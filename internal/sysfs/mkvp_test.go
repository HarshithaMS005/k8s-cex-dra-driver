package sysfs

import (
	"os"
	"path/filepath"
	"testing"
)

// The payload fixtures follow the forms captured from CEX6 queues on a
// 6.19 kernel: the genuine forms from queues answering normally, the
// placeholder forms from reads whose card query was interrupted by a
// signal (dev/hack/mkvp-min.c, strategy go-real-sigurg).

const genuineCCA = `AES NEW: empty 0x0000000000000000
AES CUR: invalid 0x0000000000000000
AES OLD: invalid 0x0000000000000000
APKA NEW: empty 0x0000000000000000
APKA CUR: valid 0x755cb8e7740dae51
APKA OLD: invalid 0x0000000000000000
ASYM NEW: empty -
ASYM CUR: empty -
ASYM OLD: empty -
`

const genuineEP11 = `WK CUR: valid 0xef490ddfce10b330b86cfe6db2ae2db98d65e8c19d9cb7a1b378dec93e398eb0
WK NEW: empty -
`

const placeholderCCA = `AES NEW: - -
AES CUR: - -
AES OLD: - -
APKA NEW: - -
APKA CUR: - -
APKA OLD: - -
ASYM NEW: - -
ASYM CUR: - -
ASYM OLD: - -
`

// The CCA info is two sequential card queries; a signal during the second
// leaves AES/ASYM real and only APKA dashed.
const mixedCCA = `AES NEW: empty 0x0000000000000000
AES CUR: valid 0x0123456789abcdef
AES OLD: invalid 0x0000000000000000
APKA NEW: - -
APKA CUR: - -
APKA OLD: - -
ASYM NEW: empty -
ASYM CUR: empty -
ASYM OLD: empty -
`

const placeholderEP11 = `WK CUR: - -
WK NEW: - -
`

func mkvpsFixture(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mkvps"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestParseMKVPsGenuineCCA(t *testing.T) {
	mkvps, err := ParseMKVPs(mkvpsFixture(t, genuineCCA), "cca")
	if err != nil {
		t.Fatalf("ParseMKVPs: %v", err)
	}
	if got := mkvps["cex.cca.domain.ibm.com/mkvp_apka_cur"]; got != "755cb8e7740dae51" {
		t.Errorf("apka_cur = %q, want %q", got, "755cb8e7740dae51")
	}
	if got := mkvps["cex.cca.domain.ibm.com/mkvp_aes_new"]; got != "0000000000000000" {
		t.Errorf("aes_new = %q, want the genuine all-zero pattern", got)
	}
	// "empty -" is a genuine state with no pattern, not a placeholder.
	if got, ok := mkvps["cex.cca.domain.ibm.com/mkvp_asym_cur"]; !ok || got != "" {
		t.Errorf("asym_cur = %q (present=%v), want present and empty", got, ok)
	}
}

func TestParseMKVPsGenuineEP11(t *testing.T) {
	mkvps, err := ParseMKVPs(mkvpsFixture(t, genuineEP11), "ep11")
	if err != nil {
		t.Fatalf("ParseMKVPs: %v", err)
	}
	// The 64-hex-char WKVP is truncated to the 32 chars key blobs carry.
	if got := mkvps["cex.ep11.domain.ibm.com/mkvp_wk_cur"]; got != "ef490ddfce10b330b86cfe6db2ae2db9" {
		t.Errorf("wk_cur = %q, want the 32-char truncation", got)
	}
	if got := mkvps["cex.ep11.domain.ibm.com/mkvp_wk_new"]; got != "" {
		t.Errorf("wk_new = %q, want empty", got)
	}
}

func TestParseMKVPsPlaceholderPayloadFails(t *testing.T) {
	for name, tc := range map[string]struct {
		content  string
		cardType string
	}{
		"cca all placeholder":  {placeholderCCA, "cca"},
		"cca mixed":            {mixedCCA, "cca"},
		"ep11 all placeholder": {placeholderEP11, "ep11"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseMKVPs(mkvpsFixture(t, tc.content), tc.cardType); err == nil {
				t.Fatal("ParseMKVPs = nil error, want the placeholder rejection")
			}
		})
	}
}

// A dashed line under a key the card type does not publish is ignored like
// any other unknown key, not treated as a placeholder.
func TestParseMKVPsPlaceholderOnUnknownKeyIgnored(t *testing.T) {
	content := genuineEP11 + "FUTURE KEY: - -\n"
	mkvps, err := ParseMKVPs(mkvpsFixture(t, content), "ep11")
	if err != nil {
		t.Fatalf("ParseMKVPs: %v", err)
	}
	if len(mkvps) != 2 {
		t.Errorf("len = %d, want 2", len(mkvps))
	}
}
