package rosapi

import (
	"sort"
	"testing"

	"context"

	"github.com/stretchr/testify/assert"
)

type mockClient struct {
	Command string
	Args    []string
	Result  Result
	Error   error

	StreamCommand string
	StreamArgs    []string
	StreamResult  StreamResult
	StreamError   error

	Closed bool
}

func (m *mockClient) Exec(ctx context.Context, command string, args ...string) (Result, error) {
	m.Command, m.Args = command, args
	return m.Result, m.Error
}

func (m *mockClient) Stream(ctx context.Context, command string, args ...string) (StreamResult, error) {
	m.StreamCommand, m.StreamArgs = command, args
	return m.StreamResult, m.StreamError
}

func (m *mockClient) Close() error {
	m.Closed = true
	return nil
}

func TestWhere(t *testing.T) {
	assert.Equal(t, "=.proplist=a1,a2", Proplist("a1", "a2"))
	assert.Equal(t, "?attr1", WhereHasAttribute("attr1"))
	assert.Equal(t, "?-attr1", WhereNotHasAttribute("attr1"))
	assert.Equal(t, "?attr1=v1", WhereAttributeEqual("attr1", "v1"))
	assert.Equal(t, "?<attr1=v1", WhereAttributeLT("attr1", "v1"))
	assert.Equal(t, "?>attr1=v1", WhereAttributeGT("attr1", "v1"))
}

func sorted(ss []string) []string {
	sort.Strings(ss)
	return ss
}

func TestBaseAPI(t *testing.T) {
	client := &mockClient{}
	api := &baseAPI{client}
	ctx := context.Background()
	t.Run("TestAdd", func(t *testing.T) {
		api.Add(ctx, "/r1", Attrs{"k1": "v1", "k2": "v2"})
		assert.Equal(t, "/r1/add", client.Command)
		assert.Equal(t, sorted([]string{"=k1=v1", "=k2=v2"}), sorted(client.Args))
	})
	t.Run("TestSet", func(t *testing.T) {
		api.Set(ctx, "/r1", Attrs{"k1": "v1", "k2": "v2"})
		assert.Equal(t, "/r1/set", client.Command)
		assert.Equal(t, sorted([]string{"=k1=v1", "=k2=v2"}), sorted(client.Args))
	})
	t.Run("TestSetByID", func(t *testing.T) {
		api.SetByID(ctx, "/r1", "*1", Attrs{"k1": "v1", "k2": "v2"})
		assert.Equal(t, "/r1/set", client.Command)
		assert.Equal(t, sorted([]string{"=k1=v1", "=k2=v2", "=.id=*1"}), sorted(client.Args))
	})
	t.Run("TestPrint", func(t *testing.T) {
		api.Print(ctx, "/r1", WhereAttributeEqual("k1", "v1"))
		assert.Equal(t, "/r1/print", client.Command)
		assert.Equal(t, []string{"?k1=v1"}, client.Args)
	})
	t.Run("TestRemove", func(t *testing.T) {
		api.Remove(ctx, "/r1", "*1")
		assert.Equal(t, "/r1/remove", client.Command)
		assert.Equal(t, []string{"=.id=*1"}, client.Args)
	})
}
