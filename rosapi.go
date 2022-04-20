package rosapi

import (
	"context"
	"crypto/tls"
	"path"
	"strings"
	"time"
)

const (
	IDAttrName = ".id"

	ProplistName = ".proplist"
)

type Attrs map[string]string

func (a Attrs) ID() string {
	return a[IDAttrName]
}

func Proplist(name ...string) string {
	return Attribute(ProplistName, strings.Join(name, ","))
}

func Attribute(name, value string) string {
	return "=" + name + "=" + value
}

func WhereHasAttribute(name string) string {
	return "?" + name
}

func WhereNotHasAttribute(name string) string {
	return "?-" + name
}

func WhereAttributeEqual(name, value string) string {
	return "?" + name + "=" + value
}

func WhereAttributeLT(name, value string) string {
	return "?<" + name + "=" + value
}

func WhereAttributeGT(name, value string) string {
	return "?>" + name + "=" + value
}

func IDAttrs(id string, kvs ...string) Attrs {
	attrs := make(Attrs)
	attrs[IDAttrName] = id
	for i := 0; i < len(kvs)/2; i += 2 {
		attrs[kvs[i]] = kvs[i+1]
	}
	return attrs
}

type Result []Attrs

type StreamResult interface {
	Next() (Attrs, error)
	Close() error
}

type ROSAPI interface {
	ROSClient
	Add(ctx context.Context, resource string, attrs Attrs) error
	Set(ctx context.Context, resource string, attrs Attrs) error
	SetByID(ctx context.Context, resource, id string, attrs Attrs) error
	Print(ctx context.Context, resource string, wheres ...string) (Result, error)
	Remove(ctx context.Context, resource string, id string) error 
}

type ROSClient interface {
	Exec(ctx context.Context, command string, args ...string) (Result, error)
	Stream(ctx context.Context, commond string, args ...string) (StreamResult, error)
	Close() error
}

type baseAPI struct {
	ROSClient
}

func (r *baseAPI) Add(ctx context.Context, resource string, attrs Attrs) error {
	args := make([]string, 0, len(attrs))
	for k, v := range attrs {
		args = append(args, Attribute(k, v))
	}
	resource = path.Join(resource, "add")
	_, err := r.Exec(ctx, resource, args...)
	return err
}

func (r *baseAPI) SetByID(ctx context.Context, resource, id string, attrs Attrs) error {
	attrs[IDAttrName] = id
	return r.Set(ctx, resource, attrs)
}

func (r *baseAPI) Set(ctx context.Context, resource string, attrs Attrs) error {
	args := make([]string, 0, len(attrs))
	for k, v := range attrs {
		args = append(args, Attribute(k, v))
	}
	resource = path.Join(resource, "set")
	_, err := r.Exec(ctx, resource, args...)
	return err
}

func (r *baseAPI) Print(ctx context.Context, resource string, wheres ...string) (Result, error) {
	resource = path.Join(resource, "print")
	result, err := r.Exec(ctx, resource, wheres...)
	return result, err
}

func (r *baseAPI) Remove(ctx context.Context, resource string, id string) error {
	resource = path.Join(resource, "remove")
	_, err := r.Exec(ctx, resource, Attribute(IDAttrName, id))
	return err
}

type rosAPI struct {
	opts *options
	cc   ClientConn
}

type options struct {
	DialTimeout time.Duration
	TLSConfig   *tls.Config
}

var defaultOptions = &options{
	DialTimeout: 15 * time.Second,
}

type Option func(opts *options)

func re2Map(re Re) Attrs {
	ret := make(Attrs)
	for _, r := range re {
		// NOTE: ignore not attribute word
		if r[0] != '=' {
			continue
		}
		ss := strings.SplitN(r[1:], "=", 2)
		ret[ss[0]] = ss[1]
	}
	return ret
}

func (r *rosAPI) Exec(ctx context.Context, command string, args ...string) (Result, error) {
	output, err := r.cc.Exec(ctx, command, args...)
	if err != nil {
		return nil, err
	}
	result := make(Result, 0, len(output))
	for _, re := range output {
		result = append(result, re2Map(re))
	}
	return result, nil
}

type streamResult struct {
	sout StreamOuput
}

func (s *streamResult) Next() (Attrs, error) {
	re, err := s.sout.Recv()
	if err != nil {
		return nil, err
	}
	return re2Map(re), nil
}

func (s *streamResult) Close() error {
	return s.sout.Close()
}

func (r *rosAPI) Stream(ctx context.Context, commond string, args ...string) (StreamResult, error) {
	sout, err := r.cc.Stream(ctx, commond, args...)
	if err != nil {
		return nil, err
	}
	return &streamResult{sout: sout}, nil
}

func (r *rosAPI) Close() error {
	return r.cc.Close()
}

func New(target string, optsFn ...Option) (ROSAPI, error) {
	opts := *defaultOptions
	for _, fn := range optsFn {
		fn(&opts)
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.DialTimeout)
	defer cancel()

	api := &rosAPI{opts: &opts}
	var err error
	if opts.TLSConfig == nil {
		api.cc, err = DialContext(ctx, target)
	} else {
		api.cc, err = DialTLSContext(ctx, target, api.opts.TLSConfig)
	}
	return &baseAPI{api}, err
}
