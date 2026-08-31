package sysfs

import (
	"strings"
	"testing"
)

const sysinfoSample = `Manufacturer:         IBM
Type:                 3931
LIC Identifier:       1234567890abcdef
Model:                704              A01
Sequence Code:        00000000000A8F67
Plant:                02
Model Capacity:       704              00000000
`

func TestMachineIDFromSysinfo(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{
			name:    "full sysinfo",
			content: sysinfoSample,
			want:    "ibm-3931-02-00000000000a8f67",
		},
		{
			name:    "missing manufacturer",
			content: strings.Replace(sysinfoSample, "Manufacturer:         IBM\n", "", 1),
			wantErr: true,
		},
		{
			name:    "missing type",
			content: strings.Replace(sysinfoSample, "Type:                 3931\n", "", 1),
			wantErr: true,
		},
		{
			name:    "missing plant",
			content: strings.Replace(sysinfoSample, "Plant:                02\n", "", 1),
			wantErr: true,
		},
		{
			name:    "missing sequence code",
			content: strings.Replace(sysinfoSample, "Sequence Code:        00000000000A8F67\n", "", 1),
			wantErr: true,
		},
		{
			name:    "empty content",
			content: "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := machineIDFromSysinfo("/proc/sysinfo", tt.content)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("machineIDFromSysinfo() = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("machineIDFromSysinfo() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("machineIDFromSysinfo() = %q, want %q", got, tt.want)
			}
		})
	}
}
