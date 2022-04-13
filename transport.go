package rosapi

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
)

type replyType string

const (
	replyDone  replyType = "!done"
	replyTrap  replyType = "!trap"
	replyRe    replyType = "!re"
	replyFatal replyType = "!fatal"
)

type reply struct {
	Type     replyType
	Sentence []string
}

type transport struct {
	conn net.Conn
	dec  *sentenceDecoder
}

func writeAll(w io.Writer, b []byte) error {
	for len(b) != 0 {
		if n, err := w.Write(b); err != nil {
			return err
		} else {
			b = b[n:]
		}
	}
	return nil
}

func (r *transport) Send(words ...string) error {
	buf := &bytes.Buffer{}
	if err := (&sentenceEncoder{w: buf}).Encode(words); err != nil {
		return err
	}
	return writeAll(r.conn, buf.Bytes())
}

func (r *transport) Recv() (*reply, error) {
	if r.dec == nil {
		r.dec = &sentenceDecoder{r: r.conn}
	}

	word, err := r.dec.Decode()
	if err != nil {
		return nil, err
	}
	reply := &reply{Type: replyType(word)}
	for err == nil && len(word) != 0 {
		if word, err = r.dec.Decode(); err == nil && len(word) > 0 {
			reply.Sentence = append(reply.Sentence, string(word))
		}
	}
	return reply, nil
}

type sentenceEncoder struct {
	w io.Writer
}

func (s *sentenceEncoder) Encode(words []string) error {
	for _, word := range words {
		if err := s.writeWord(word); err != nil {
			return err
		}
	}
	return s.writeWord("")
}

func (s *sentenceEncoder) writeWord(word string) error {
	if err := s.writeLen(len(word)); err != nil || len(word) == 0 {
		return err
	}
	return writeAll(s.w, []byte(word))
}

func (s *sentenceEncoder) writeLen(l int) error {
	var off int
	switch {
	case l < 0x80:
		off = 4
	case l < 0x4000:
		l |= 0x8000
		off = 3
	case l < 0x200000:
		l |= 0xC00000
		off = 2
	case l < 0x10000000:
		l |= 0xE0000000
		off = 1
	}
	b := []byte{0xF0, 0x0, 0x0, 0x0, 0x0}
	binary.BigEndian.PutUint32(b[1:], uint32(l))
	return writeAll(s.w, b[off:])
}

type sentenceDecoder struct {
	r io.Reader
}

func (s *sentenceDecoder) Decode() ([]byte, error) {
	b := make([]byte, 5)
	var err error
	if _, err = io.ReadFull(s.r, b[:1]); err != nil {
		return nil, err
	}
	switch {
	case b[0]&0xF0 == 0xF0: // 5byte
		b = b[1:]
		_, err = io.ReadFull(s.r, b)
	case b[0]&0xE0 == 0xE0: // 4byte
		b[0] = b[0] ^ 0xE0
		_, err = io.ReadFull(s.r, b[1:4])
	case b[0]&0xC0 == 0xC0: // 3byte
		b[0] = b[0] ^ 0xC0
		_, err = io.ReadFull(s.r, b[1:3])
	case b[0]&0x80 == 0x80: // 2byte
		b[0] = b[0] ^ 0x80
		_, err = io.ReadFull(s.r, b[1:2])
	}
	if err != nil {
		return nil, err
	}
	l := binary.LittleEndian.Uint32(b)
	v := make([]byte, l)
	_, err = io.ReadFull(s.r, v)
	return v, err
}
