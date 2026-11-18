package ipfix

import (
	"encoding/hex"
	"strconv"
	"time"
)

// Format writes a Value as text for people: numbers in decimal, addresses and MACs in
// their usual notation, times in RFC 3339 (UTC) and raw bytes in hex.
func (v Value) Format() string {
	switch v.Kind {
	case KindUnsigned:
		return strconv.FormatUint(v.Uint, 10)
	case KindSigned:
		return strconv.FormatInt(v.Int, 10)
	case KindFloat:
		return strconv.FormatFloat(v.Float, 'g', -1, 64)
	case KindBool:
		return strconv.FormatBool(v.Bool)
	case KindMAC:
		return v.MAC.String()
	case KindAddr:
		return v.Addr.String()
	case KindString:
		return v.String
	case KindTime:
		return v.Time.UTC().Format(time.RFC3339Nano)
	}
	return hex.EncodeToString(v.Octets)
}
