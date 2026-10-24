package plugin

import (
	"context"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/vmihailenco/msgpack/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb5 "github.com/pravo-systems/pravo/internal/tfplugin/tfplugin5"
	pb6 "github.com/pravo-systems/pravo/internal/tfplugin/tfplugin6"
)

var listScope = tftypes.Object{AttributeTypes: map[string]tftypes.Type{"region": tftypes.String}}

func identityBytes(t *testing.T, m map[string]any) []byte {
	t.Helper()
	b, err := msgpack.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// serveList starts a gRPC server whose only method is the server-streaming
// ListResource of the given service, and returns a connection to it.
func serveList(t *testing.T, service string, handler func(grpc.ServerStream) error) *grpc.ClientConn {
	t.Helper()
	srv := grpc.NewServer()
	srv.RegisterService(&grpc.ServiceDesc{
		ServiceName: service,
		HandlerType: (*any)(nil),
		Streams: []grpc.StreamDesc{{
			StreamName:    "ListResource",
			ServerStreams: true,
			Handler:       func(_ any, st grpc.ServerStream) error { return handler(st) },
		}},
	}, struct{}{})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)
	cc, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cc.Close() })
	return cc
}

func scope(region string) tftypes.Value {
	return tftypes.NewValue(listScope, map[string]tftypes.Value{"region": tftypes.NewValue(tftypes.String, region)})
}

func TestListResourceV6StreamsEvents(t *testing.T) {
	var got *pb6.ListResource_Request
	cc := serveList(t, "tfplugin6.Provider", func(st grpc.ServerStream) error {
		got = new(pb6.ListResource_Request)
		if err := st.RecvMsg(got); err != nil {
			return err
		}
		for i, id := range []string{"i-1", "i-2", "i-3"} {
			ev := &pb6.ListResource_Event{
				Identity:    &pb6.ResourceIdentityData{IdentityData: &pb6.DynamicValue{Msgpack: identityBytes(t, map[string]any{"id": id, "n": i})}},
				DisplayName: "name-" + id,
			}
			if err := st.SendMsg(ev); err != nil {
				return err
			}
		}
		// A warning that comes on an event of its own.
		return st.SendMsg(&pb6.ListResource_Event{Diagnostic: []*pb6.Diagnostic{{Severity: pb6.Diagnostic_WARNING, Summary: "partial"}}})
	})
	p := &v6{c: pb6.NewProviderClient(cc), close: func() {}}
	out, diags, err := p.ListResource(context.Background(), "aws_instance", scope("eu"), 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []ListedResource{
		{Identity: map[string]any{"id": "i-1", "n": int64(0)}, DisplayName: "name-i-1"},
		{Identity: map[string]any{"id": "i-2", "n": int64(1)}, DisplayName: "name-i-2"},
		{Identity: map[string]any{"id": "i-3", "n": int64(2)}, DisplayName: "name-i-3"},
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("listed %#v\nwant %#v", out, want)
	}
	if len(diags) != 1 || diags[0].Summary != "partial" || diags[0].Severity != "warning" {
		t.Fatalf("diagnostics %#v", diags)
	}
	if got.TypeName != "aws_instance" || got.Limit != 0 || got.IncludeResourceObject {
		t.Fatalf("request %+v", got)
	}
	v, err := tftypes.ValueFromMsgPack(got.Config.Msgpack, listScope)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]tftypes.Value
	var region string
	if v.As(&m) != nil || m["region"].As(&region) != nil || region != "eu" {
		t.Fatalf("the scope reached the provider as %v", v)
	}
}

func TestListResourceV5StopsAtTheLimit(t *testing.T) {
	sent := 0
	cc := serveList(t, "tfplugin5.Provider", func(st grpc.ServerStream) error {
		var req pb5.ListResource_Request
		if err := st.RecvMsg(&req); err != nil {
			return err
		}
		if req.Limit != 2 {
			t.Errorf("limit %d", req.Limit)
		}
		for i := 0; i < 100; i++ {
			ev := &pb5.ListResource_Event{
				Identity:    &pb5.ResourceIdentityData{IdentityData: &pb5.DynamicValue{Msgpack: identityBytes(t, map[string]any{"id": i})}},
				DisplayName: "n",
			}
			if err := st.SendMsg(ev); err != nil {
				return nil // the client went away
			}
			sent++
		}
		return nil
	})
	p := &v5{c: pb5.NewProviderClient(cc), close: func() {}}
	out, _, err := p.ListResource(context.Background(), "t", scope("eu"), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("a limit of 2 returned %d objects", len(out))
	}
}

func TestListResourceError(t *testing.T) {
	cc := serveList(t, "tfplugin6.Provider", func(st grpc.ServerStream) error {
		return context.DeadlineExceeded
	})
	p := &v6{c: pb6.NewProviderClient(cc), close: func() {}}
	if _, _, err := p.ListResource(context.Background(), "t", scope("eu"), 0); err == nil {
		t.Fatal("a stream that fails was read as an empty list")
	}
}

func TestDecodeIdentityNormalizes(t *testing.T) {
	b := identityBytes(t, map[string]any{"a": int8(3), "b": uint16(70), "c": []any{int32(1), "x"}, "d": map[string]any{"e": 1.5}})
	got, err := decodeIdentity(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"a": int64(3), "b": int64(70), "c": []any{int64(1), "x"}, "d": map[string]any{"e": 1.5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%#v", got)
	}
	if m, err := decodeIdentity(nil, []byte(`{"id":"x"}`)); err != nil || m["id"] != "x" {
		t.Fatalf("json identity: %v %v", m, err)
	}
	if _, err := decodeIdentity(identityBytes(t, nil)[:0], nil); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeIdentity([]byte{0x92, 0x01, 0x02}, nil); err == nil {
		t.Fatal("an identity that is a list was accepted")
	}
}

type listingProvider struct {
	recordedProvider
	calls int
}

func (l *listingProvider) ListResource(_ context.Context, typ string, cfg tftypes.Value, limit int64) ([]ListedResource, Diagnostics, error) {
	l.calls++
	return []ListedResource{{Identity: map[string]any{"id": "i-1", "n": int64(2)}, DisplayName: "one"}}, Diagnostics{{Severity: "warning", Summary: "partial"}}, nil
}

// A list is recorded and replays, with its identities, its names and its warnings, and
// a list that was not recorded is refused.
func TestRecordingOfAListReplays(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rec", "thing")
	inner := &listingProvider{}
	r, err := NewRecorder(inner, dir)
	if err != nil {
		t.Fatal(err)
	}
	want, wantDiags, err := r.ListResource(context.Background(), "aws_instance", scope("eu"), 10)
	if err != nil || len(want) != 1 || inner.calls != 1 {
		t.Fatalf("%v %v %d", want, err, inner.calls)
	}
	rp, err := NewReplayer(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, gotDiags, err := rp.ListResource(context.Background(), "aws_instance", scope("eu"), 10)
	if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotDiags, wantDiags) {
		t.Errorf("replayed %#v %#v %v, want %#v %#v", got, gotDiags, err, want, wantDiags)
	}
	if _, _, err := rp.ListResource(context.Background(), "aws_instance", scope("us"), 10); err == nil || !strings.Contains(err.Error(), ErrNoRecording.Error()) {
		t.Errorf("a list in another scope: %v", err)
	}
	if _, _, err := rp.ListResource(context.Background(), "aws_instance", scope("eu"), 5); err == nil {
		t.Error("a list with another limit was replayed")
	}
}
