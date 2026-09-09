package mysql

import (
	"bytes"
	"testing"
)

func TestIssue261BinaryValuesUseNativeWireEncoding(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		definition columnMetadata
		want       []byte
	}{
		{name: "tiny signed", value: "-42", definition: columnMetadata{typ: mysqlTypeTiny}, want: []byte{0xd6}},
		{name: "tiny unsigned", value: "255", definition: columnMetadata{typ: mysqlTypeTiny, flags: mysqlUnsignedFlag}, want: []byte{0xff}},
		{name: "small signed", value: "-32000", definition: columnMetadata{typ: mysqlTypeShort}, want: []byte{0x00, 0x83}},
		{name: "small unsigned", value: "65535", definition: columnMetadata{typ: mysqlTypeShort, flags: mysqlUnsignedFlag}, want: []byte{0xff, 0xff}},
		{name: "medium signed", value: "-8388608", definition: columnMetadata{typ: mysqlTypeInt24}, want: []byte{0x00, 0x00, 0x80, 0xff}},
		{name: "medium unsigned", value: "16777215", definition: columnMetadata{typ: mysqlTypeInt24, flags: mysqlUnsignedFlag}, want: []byte{0xff, 0xff, 0xff, 0x00}},
		{name: "float", value: "1.25", definition: columnMetadata{typ: mysqlTypeFloat}, want: []byte{0x00, 0x00, 0xa0, 0x3f}},
		{name: "bit", value: "10", definition: columnMetadata{typ: mysqlTypeBit, length: 8}, want: []byte{1, 10}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := encodeBinaryValue(test.value, test.definition)
			if err != nil || !bytes.Equal(got, test.want) {
				t.Fatalf("encodeBinaryValue(%q, %#x) = %x err %v, want %x", test.value, test.definition.typ, got, err, test.want)
			}
		})
	}
}
