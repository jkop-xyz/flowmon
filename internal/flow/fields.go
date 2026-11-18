package flow

import "encoding/hex"

// FieldView is one stored field of a Flow, described for people.
type FieldView struct {
	Enterprise uint32 `json:"enterprise,omitempty"`
	ID         uint16 `json:"id"`
	// Name is the IANA name, or a placeholder such as enterprise14988.ie1 for fields
	// IANA doesn't define.
	Name string `json:"name"`
	// Known is set when IANA defines the field, so Value is decoded by its type.
	Known bool `json:"known"`
	// Type is the IANA abstract data type; empty for unknown fields.
	Type  string `json:"type,omitempty"`
	Value string `json:"value"`
	// Raw is the bytes as received, in hex.
	Raw string `json:"raw"`
	// Error says why the bytes don't fit the field's type; Value is then the raw hex.
	Error string `json:"error,omitempty"`
}

// DescribeFields decodes a Flow's stored fields (see EncodeFields), in their original
// order.
func DescribeFields(encoded []byte) ([]FieldView, error) {
	fields, err := DecodeFields(encoded)
	if err != nil {
		return nil, err
	}
	out := make([]FieldView, 0, len(fields))
	for _, f := range fields {
		v := FieldView{Enterprise: f.Spec.Enterprise, ID: f.Spec.ID, Name: f.Spec.Name(), Raw: hex.EncodeToString(f.Raw)}
		if e, ok := f.Spec.Element(); ok {
			v.Known, v.Type = true, e.Type.String()
		}
		val, err := f.Value()
		if err != nil {
			v.Error = err.Error()
		}
		v.Value = val.Format()
		out = append(out, v)
	}
	return out, nil
}
