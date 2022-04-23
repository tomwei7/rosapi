package rosapi

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Re []string

type StreamOuput interface {
	Recv() (Re, error)
	Close() error
}

type Output []Re

type ClientConn interface {
	Exec(ctx context.Context, command string, args ...string) (Output, error)
	Stream(ctx context.Context, commond string, args ...string) (StreamOuput, error)
	Close() error
}

type clientConn struct {
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)

	addr     string
	userInfo *url.Userinfo

	mx    sync.Mutex
	conn  net.Conn
	trans *transport

	tsm *taggedStreamManager
}

func setDeadlineForConn(ctx context.Context, conn net.Conn) {
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
}

func parseTarget(target string) (addr string, userInfo *url.Userinfo) {
	ss := strings.SplitN(target, "@", 2)
	if len(ss) == 2 {
		addr = ss[1]
		if ss = strings.SplitN(ss[0], ":", 2); len(ss) == 2 {
			userInfo = url.UserPassword(ss[0], ss[1])
		} else {
			userInfo = url.User(ss[0])
		}
	} else {
		addr = target
	}
	return
}

func Dial(target string) (ClientConn, error) {
	return DialContext(context.Background(), target)
}

func DialTLS(target string, tlsClientConfig *tls.Config) (ClientConn, error) {
	return DialTLSContext(context.Background(), target, tlsClientConfig)
}

func DialContext(ctx context.Context, target string) (ClientConn, error) {
	cc := &clientConn{DialContext: dial}
	cc.addr, cc.userInfo = parseTarget(target)
	return cc, cc.dial(ctx)
}

func DialTLSContext(ctx context.Context, target string, tlsClientConfig *tls.Config) (ClientConn, error) {
	tlsContext := &tlsContext{cfg: tlsClientConfig}
	cc := &clientConn{DialContext: tlsContext.dialTLS}
	cc.addr, cc.userInfo = parseTarget(target)
	return cc, cc.dial(ctx)
}

func dial(ctx context.Context, network, addr string) (net.Conn, error) {
	_, _, err := net.SplitHostPort(addr)
	if strings.Contains(err.Error(), "missing port in address") {
		addr += ":8728"
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		return net.Dial(network, addr)
	}
	return net.DialTimeout(network, addr, deadline.Sub(time.Now()))
}

type tlsContext struct {
	cfg *tls.Config
}

func (t *tlsContext) dialTLS(ctx context.Context, network, addr string) (net.Conn, error) {
	_, _, err := net.SplitHostPort(addr)
	if strings.Contains(err.Error(), "missing port in address") {
		addr += ":8729"
	}

	var conn net.Conn
	deadline, ok := ctx.Deadline()
	if !ok {
		conn, err = net.Dial(network, addr)
	} else {
		conn, err = net.DialTimeout(network, addr, deadline.Sub(time.Now()))
	}

	if err != nil {
		return nil, err
	}

	return tls.Client(conn, t.cfg), nil
}

func (c *clientConn) Exec(ctx context.Context, command string, args ...string) (Output, error) {
	sout, err := c.newStream(ctx, command, args...)
	if err != nil {
		return nil, err
	}
	defer sout.Close()

	out := make(Output, 0, 4)
	var re Re
	for err == nil {
		re, err = sout.Recv()
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			break
		}
		out = append(out, re)
	}
	return out, err
}

func (c *clientConn) Stream(ctx context.Context, command string, args ...string) (StreamOuput, error) {
	return c.newStream(ctx, command, args...)
}

func (c *clientConn) newStream(ctx context.Context, command string, args ...string) (StreamOuput, error) {
	ts, err := c.newTagStream(ctx)
	if err != nil {
		return nil, err
	}
	words := make([]string, len(args)+1)
	words = append(words, command)
	words = append(words, args...)
	if err = ts.Send(ctx, words...); err != nil {
		return nil, err
	}
	return &streamOutput{ctx: ctx, ts: ts}, nil
}

func (c *clientConn) newTagStream(ctx context.Context) (*taggedStream, error) {
	ts, err := c.tsm.newStream()
	if err == nil {
		return ts, nil
	}

	c.mx.Lock()
	defer c.mx.Unlock()
	if ts, err = c.tsm.newStream(); err == nil {
		return ts, nil
	}
	c.conn.Close()
	if err = c.dialNotLock(ctx); err != nil {
		return nil, err
	}
	c.tsm = newTaggedStreamManager(c.trans)
	return c.tsm.newStream()
}

type streamOutput struct {
	ctx context.Context
	ts  *taggedStream
}

func (s *streamOutput) Recv() (Re, error) {
	r, err := s.ts.Recv(s.ctx)
	if err != nil {
		return nil, err
	}
	switch r.Type {
	case replyTrap:
		return nil, fmt.Errorf("ReplyError Type: %s, Sentence: %v", r.Type, r.Sentence)
	case replyFatal:
		err := fmt.Errorf("ReplyFatal Sentence: %v", r.Sentence)
		s.ts.tsm.closeWithError(err)
		return nil, err
	case replyDone:
		return nil, io.EOF
	}
	return Re(r.Sentence), nil
}

func (s *streamOutput) Close() error {
	return s.ts.Close()
}

func (c *clientConn) dial(ctx context.Context) error {
	c.mx.Lock()
	defer c.mx.Unlock()
	return c.dialNotLock(ctx)
}

func (c *clientConn) dialNotLock(ctx context.Context) error {
	conn, err := c.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return err
	}

	c.conn = conn
	c.trans = &transport{conn: c.conn}
	if err = c.loginNotLock(ctx); err != nil {
		return err
	}
	c.tsm = newTaggedStreamManager(c.trans)
	return nil
}

func (c *clientConn) loginNotLock(ctx context.Context) error {
	words := make([]string, 0, 3)
	words = append(words, "/login")
	if c.userInfo != nil {
		words = append(words, "=name="+c.userInfo.Username())
		if password, ok := c.userInfo.Password(); ok {
			words = append(words, "=password="+password)
		}
	}

	setDeadlineForConn(ctx, c.conn)
	if err := c.trans.Send(words...); err != nil {
		return err
	}

	setDeadlineForConn(ctx, c.conn)
	reply, err := c.trans.Recv()
	if err != nil {
		return err
	}

	// clear conn deadline
	c.conn.SetDeadline(time.Time{})

	switch reply.Type {
	case replyDone:
		return nil
	case replyTrap:
		if len(reply.Sentence) == 0 {
			return fmt.Errorf("invalid trap reply miss sentence")
		}
		return fmt.Errorf("LoginError: %v", reply.Sentence)
	default:
		return fmt.Errorf("recv unexpect reply on login reply type: %s, sentence: %v", reply.Type, reply.Sentence)
	}
}

func (c *clientConn) Close() error {
	c.tsm.Close()
	return c.conn.Close()
}

func encodeNamedValue(key, value string) string {
	sb := strings.Builder{}
	sb.WriteByte('=')
	sb.WriteString(key)
	sb.WriteByte('=')
	sb.WriteString(value)
	return sb.String()
}

func decodeNamedValue(pair string) (string, string, bool) {
	ss := strings.Split(pair, "=")
	if len(ss) != 3 {
		return "", "", false
	}
	return ss[1], ss[2], true
}

type taggedStream struct {
	tagID  int
	recvCh chan *reply
	tsm    *taggedStreamManager
}

type taggedStreamManager struct {
	trans *transport

	mx           sync.Mutex
	nextTagID    int
	taggedStream map[int]*taggedStream

	sendCh chan []string

	lastError error
	closeCh   chan struct{}
}

func (t *taggedStreamManager) startSend() {
	for words := range t.sendCh {
		if err := t.trans.Send(words...); err != nil {
			t.closeWithError(fmt.Errorf("send to transport error: %w", err))
		}
	}
}

func (t *taggedStreamManager) closeWithError(err error) {
	t.mx.Lock()
	defer t.mx.Unlock()
	if t.lastError != nil {
		return
	}
	t.lastError = err
	close(t.closeCh)
}

func (t *taggedStreamManager) startRecv() {
	for {
		r, err := t.trans.Recv()
		if err != nil {
			t.closeWithError(fmt.Errorf("recv from transport error: %w", err))
			return
		}
		t.dispatchReply(r)
	}
}

func (t *taggedStreamManager) dispatchReply(r *reply) {
	tagPrefix := ".tag="
	var tag string
	for _, v := range r.Sentence {
		if strings.HasPrefix(v, ".tag=") {
			tag = v[len(tagPrefix):]
		}
	}
	tagID, _ := strconv.Atoi(tag)
	if tagID == 0 {
		return
	}

	t.mx.Lock()
	ts, ok := t.taggedStream[tagID]
	t.mx.Unlock()

	if !ok {
		t.cancelTag(tagID)
		return
	}
	ts.recvCh <- r
}

func (t *taggedStreamManager) cancelTag(tagID int) error {
	var err error
	cancelTimeout := time.NewTimer(time.Second)
	select {
	case t.sendCh <- []string{"/cancel", "=tag=" + strconv.Itoa(tagID)}:
	case <-cancelTimeout.C:
		err = fmt.Errorf("CancelTag Timeout")
	case <-t.closeCh:
		err = t.lastError
	}

	t.mx.Lock()
	delete(t.taggedStream, tagID)
	t.mx.Unlock()
	return err
}

func (t *taggedStreamManager) newStream() (*taggedStream, error) {
	t.mx.Lock()
	defer t.mx.Unlock()
	if t.lastError != nil {
		return nil, t.lastError
	}

	tagID := t.nextTagID
	t.nextTagID++

	ts := &taggedStream{tagID: tagID, recvCh: make(chan *reply), tsm: t}
	t.taggedStream[tagID] = ts
	return ts, nil
}

func (t *taggedStreamManager) Close() error {
	t.closeWithError(fmt.Errorf("ManualClosed"))
	return nil
}

func (t *taggedStream) Send(ctx context.Context, words ...string) error {
	words = append(words, ".tag="+strconv.Itoa(t.tagID))
	select {
	case t.tsm.sendCh <- words:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.tsm.closeCh:
		return t.tsm.lastError
	}
}

func (t *taggedStream) Recv(ctx context.Context) (*reply, error) {
	select {
	case r := <-t.recvCh:
		return r, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.tsm.closeCh:
		return nil, t.tsm.lastError
	}
}

func (t *taggedStream) Close() error {
	return t.tsm.cancelTag(t.tagID)
}

func newTaggedStreamManager(trans *transport) *taggedStreamManager {
	tsm := &taggedStreamManager{
		trans: trans,

		nextTagID:    1,
		taggedStream: make(map[int]*taggedStream),

		sendCh:  make(chan []string),
		closeCh: make(chan struct{}),
	}
	go tsm.startRecv()
	go tsm.startSend()
	return tsm
}
