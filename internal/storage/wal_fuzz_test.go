package storage

import "testing"

func FuzzWALPayloadRoundTrip(f *testing.F) {
	f.Add("app", "items", "alpha", "beta")
	f.Add("app", "items", "left\x01right", "tail")
	f.Add("app", "items", "", "")
	f.Fuzz(func(t *testing.T, namespace, name, first, second string) {
		payload := encodePayload(walInsert, namespace, name, []string{first, second})
		kind, gotNamespace, gotName, row, err := decodePayload(payload)
		if err != nil {
			t.Fatalf("decode encoded payload: %v", err)
		}
		if kind != walInsert || gotNamespace != namespace || gotName != name || len(row) != 2 || row[0] != first || row[1] != second {
			t.Fatalf("round trip = kind=%d namespace=%q name=%q row=%q", kind, gotNamespace, gotName, row)
		}
	})
}

func FuzzWALPayloadDecodeNeverPanics(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{walPayloadMagic})
	f.Add([]byte{walInsert, 'a', 0, 'b'})
	f.Fuzz(func(t *testing.T, payload []byte) {
		_, _, _, _, _ = decodePayload(payload)
	})
}
