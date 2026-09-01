package mysql

import (
	"bytes"
	"strconv"
	"testing"
)

func TestIssue231BitResultUsesPackedBytes(t *testing.T) {
	tests := []struct {
		name  string
		width uint32
		value string
		want  []byte
	}{
		{name: "bit 1 zero", width: 1, value: "0", want: []byte{1, 0}},
		{name: "bit 1 one", width: 1, value: "1", want: []byte{1, 1}},
		{name: "bit 4", width: 4, value: "10", want: []byte{1, 0x0a}},
		{name: "bit 8 high", width: 8, value: "128", want: []byte{1, 0x80}},
		{name: "bit 9 high", width: 9, value: "256", want: []byte{2, 1, 0}},
		{name: "bit 16 all", width: 16, value: "65535", want: []byte{2, 0xff, 0xff}},
		{name: "bit 63 high", width: 63, value: "4611686018427387904", want: append([]byte{8, 0x40}, make([]byte, 7)...)},
		{name: "bit 64 high", width: 64, value: "9223372036854775808", want: append([]byte{8, 0x80}, make([]byte, 7)...)},
		{name: "bit 64 all", width: 64, value: "18446744073709551615", want: append([]byte{8}, bytes.Repeat([]byte{0xff}, 8)...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := encodeBinaryValue(test.value, columnMetadata{typ: mysqlTypeBit, length: test.width})
			if err != nil || !bytes.Equal(got, test.want) {
				t.Fatalf("encodeBinaryValue(%q, BIT(%d)) = %x err %v, want %x", test.value, test.width, got, err, test.want)
			}
		})
	}
}

func TestIssue231TextBitResultUsesPackedBytes(t *testing.T) {
	tests := []struct {
		name  string
		width uint32
		value string
		want  []byte
	}{
		{name: "zero", width: 1, value: "0", want: []byte{1, 0}},
		{name: "low bits", width: 4, value: "10", want: []byte{1, 0x0a}},
		{name: "high bit", width: 9, value: "256", want: []byte{2, 1, 0}},
		{name: "sixty four bits", width: 64, value: "9223372036854775808", want: append([]byte{8, 0x80}, make([]byte, 7)...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := textRowWithDefinitions([]string{test.value}, 0, nil, []columnMetadata{{typ: mysqlTypeBit, length: test.width}})
			if err != nil || !bytes.Equal(got, test.want) {
				t.Fatalf("textRowWithDefinitions(%q, BIT(%d)) = %x err %v, want %x", test.value, test.width, got, err, test.want)
			}
		})
	}
}

func TestIssue231BitResultSupportsEveryDeclaredWidth(t *testing.T) {
	for width := uint32(1); width <= 64; width++ {
		t.Run("BIT("+formatIssue231Width(width)+")", func(t *testing.T) {
			value := uint64(1) << uint(width-1)
			byteCount := int((width + 7) / 8)
			want := make([]byte, byteCount+1)
			want[0] = byte(byteCount)
			want[1] = byte(1 << uint((width-1)%8))
			got, err := encodeBinaryValue(formatIssue231Uint(value), columnMetadata{typ: mysqlTypeBit, length: width})
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("encodeBinaryValue(%d, BIT(%d)) = %x err %v, want %x", value, width, got, err, want)
			}
		})
	}
}

func formatIssue231Width(width uint32) string {
	return strconv.Itoa(int(width))
}

func formatIssue231Uint(value uint64) string {
	return strconv.FormatUint(value, 10)
}
