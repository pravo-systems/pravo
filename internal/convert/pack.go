package convert

import "github.com/hashicorp/terraform-plugin-go/tftypes"

// Pack encodes a value as msgpack, the form that a saved plan and a recording keep. A value with
// no type (the zero Value) packs to nothing. A value that cannot be encoded is an error: the
// caller says what it does about it, none is swallowed here.
func Pack(v tftypes.Value) ([]byte, error) {
	if v.Type() == nil {
		return nil, nil
	}
	return v.MarshalMsgPack(v.Type())
}

// Unpack is the other way. Nothing unpacks to the zero Value, and bytes that do not decode as
// the type are an error, never a null that stands in for them.
func Unpack(data []byte, t tftypes.Type) (tftypes.Value, error) {
	if len(data) == 0 {
		return tftypes.Value{}, nil
	}
	return tftypes.ValueFromMsgPack(data, t)
}
