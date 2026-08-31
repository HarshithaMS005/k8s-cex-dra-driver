package sysfs

import (
	"fmt"
	"path/filepath"
	"strings"
)

var ccaMKVPKeyMap = map[string]string{
	"AES CUR":  "cex.cca.domain.ibm.com/mkvp_aes_cur",
	"AES OLD":  "cex.cca.domain.ibm.com/mkvp_aes_old",
	"AES NEW":  "cex.cca.domain.ibm.com/mkvp_aes_new",
	"APKA CUR": "cex.cca.domain.ibm.com/mkvp_apka_cur",
	"APKA OLD": "cex.cca.domain.ibm.com/mkvp_apka_old",
	"APKA NEW": "cex.cca.domain.ibm.com/mkvp_apka_new",
	"ASYM CUR": "cex.cca.domain.ibm.com/mkvp_asym_cur",
	"ASYM OLD": "cex.cca.domain.ibm.com/mkvp_asym_old",
	"ASYM NEW": "cex.cca.domain.ibm.com/mkvp_asym_new",
}

// ep11WKVPHexLen is the hex-encoded length of the EP11 wrapping key
// verification pattern (WKVP). The sysfs mkvps file exposes the full
// 32-byte SHA-256 hash (64 hex chars) of the wrapping key, but only
// the first 16 bytes (32 hex chars) are stored in EP11 key blobs and
// used by s390-tools (zkey, pvapconfig, lszcrypt) for key matching.
const ep11WKVPHexLen = 32

var ep11MKVPKeyMap = map[string]string{
	"WK CUR": "cex.ep11.domain.ibm.com/mkvp_wk_cur",
	"WK NEW": "cex.ep11.domain.ibm.com/mkvp_wk_new",
}

// MKVPUnknownValue is published as the per-key MKVP attribute value when
// the queue's mkvp_state is "unknown" (no successful read, no usable cache).
const MKVPUnknownValue = "unknown"

// ParseMKVPs reads and parses the mkvps file for an AP queue. On read
// failure it returns (nil, err) so the caller can decide between the
// cached and unknown branches. On success it returns only the keys that
// were actually present in the file - missing keys are not zero-padded.
//
// A payload carrying the kernel's placeholder form is a read failure too,
// not a success: the show callback emits it when its card query failed -
// an interrupted wait, a card the bus already flushed - yet read(2) still
// returns success. Accepting it would overwrite cached real values with
// empties and publish them as known.
func ParseMKVPs(queuePath string, cardType string) (map[string]string, error) {
	mkvpPath := filepath.Join(queuePath, "mkvps")
	content, err := ReadFile(mkvpPath)
	if err != nil {
		return nil, err
	}

	mkvps := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		raw := strings.TrimSpace(parts[1])

		switch cardType {
		case "cca":
			if attrName := ccaMKVPKeyMap[key]; attrName != "" {
				if isPlaceholderState(raw) {
					return nil, fmt.Errorf("placeholder state for %q in %s: card query failed inside a successful read", key, mkvpPath)
				}
				mkvps[attrName] = ExtractHexValue(raw)
			}
		case "ep11":
			if attrName := ep11MKVPKeyMap[key]; attrName != "" {
				if isPlaceholderState(raw) {
					return nil, fmt.Errorf("placeholder state for %q in %s: card query failed inside a successful read", key, mkvpPath)
				}
				value := ExtractHexValue(raw)
				if len(value) > ep11WKVPHexLen {
					value = value[:ep11WKVPHexLen]
				}
				mkvps[attrName] = value
			}
		}
	}

	return mkvps, nil
}

// isPlaceholderState reports whether a register's value field carries the
// kernel's placeholder form, rendered "- -": a state of "-" where a genuine
// register always names one ("valid", "invalid", "empty", ...) even when no
// key is set. One placeholder line taints the whole read rather than just
// its own key: the CCA info is gathered in two sequential card queries, so
// an interruption during the second leaves the earlier keys real and only
// the later ones dashed.
func isPlaceholderState(value string) bool {
	fields := strings.Fields(value)
	return len(fields) > 0 && fields[0] == "-"
}

// ExtractHexValue extracts the hex value after "0x" from a sysfs MKVP value string.
// Input like "valid 0x755cb8e7740dae51" returns "755cb8e7740dae51".
// Returns "" if no "0x" prefix is present (e.g. "empty -").
func ExtractHexValue(s string) string {
	idx := strings.Index(s, "0x")
	if idx < 0 {
		return ""
	}
	return s[idx+2:]
}

// UnknownMKVPs returns the per-key MKVP attribute set for a card type with
// every value set to the literal string MKVPUnknownValue. Used when a queue
// has no usable MKVP information (no successful read, no cache). Returns
// an empty map for card types without master keys (e.g. "accel").
func UnknownMKVPs(cardType string) map[string]string {
	var keys map[string]string
	switch cardType {
	case "cca":
		keys = ccaMKVPKeyMap
	case "ep11":
		keys = ep11MKVPKeyMap
	default:
		return map[string]string{}
	}
	out := make(map[string]string, len(keys))
	for _, attr := range keys {
		out[attr] = MKVPUnknownValue
	}
	return out
}
