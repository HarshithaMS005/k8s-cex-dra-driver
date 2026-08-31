package features

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"k8s.io/component-base/featuregate"
)

// One test gate per stage, so the stage semantics are exercised without
// depending on whichever project gates happen to be registered.
const (
	testAlpha      featuregate.Feature = "TestAlphaFeature"
	testBeta       featuregate.Feature = "TestBetaFeature"
	testGA         featuregate.Feature = "TestGAFeature"
	testGAUnlocked featuregate.Feature = "TestGAUnlockedFeature"
	testDeprecated featuregate.Feature = "TestDeprecatedFeature"
)

var testGates = map[featuregate.Feature]featuregate.FeatureSpec{
	testAlpha:      {Default: false, PreRelease: featuregate.Alpha},
	testBeta:       {Default: true, PreRelease: featuregate.Beta},
	testGA:         {Default: true, PreRelease: featuregate.GA, LockToDefault: true},
	testGAUnlocked: {Default: true, PreRelease: featuregate.GA},
	testDeprecated: {Default: false, PreRelease: featuregate.Deprecated},
}

// newTestRegistry builds a registry over the test gates whose warnings land in
// the returned slice instead of klog.
func newTestRegistry(t *testing.T) (*registry, *[]string) {
	t.Helper()

	r := newRegistry(testGates)
	var warnings []string
	r.warnf = func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}
	return r, &warnings
}

func TestSet(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		wantEnabled map[featuregate.Feature]bool
		wantWarning string
		wantErr     string
	}{
		{
			name:  "unset leaves every gate at its default",
			value: "",
			wantEnabled: map[featuregate.Feature]bool{
				testAlpha: false, testBeta: true, testGA: true, testDeprecated: false,
			},
		},
		{
			name:        "alpha opts in",
			value:       "TestAlphaFeature=true",
			wantEnabled: map[featuregate.Feature]bool{testAlpha: true},
		},
		{
			name:        "beta opts out",
			value:       "TestBetaFeature=false",
			wantEnabled: map[featuregate.Feature]bool{testBeta: false},
		},
		{
			name:        "GA opt-out is warned and ignored",
			value:       "TestGAFeature=false",
			wantEnabled: map[featuregate.Feature]bool{testGA: true},
			wantWarning: "TestGAFeature=false",
		},
		{
			name:        "redundant GA opt-in is warned and ignored",
			value:       "TestGAFeature=true",
			wantEnabled: map[featuregate.Feature]bool{testGA: true},
			wantWarning: "TestGAFeature=true",
		},
		{
			name:        "GA pair dropped without dropping its neighbours",
			value:       "TestGAFeature=false,TestAlphaFeature=true",
			wantEnabled: map[featuregate.Feature]bool{testGA: true, testAlpha: true},
			wantWarning: "TestGAFeature=false",
		},
		{
			name:        "unlocked GA opt-out is honored without a warning",
			value:       "TestGAUnlockedFeature=false",
			wantEnabled: map[featuregate.Feature]bool{testGAUnlocked: false},
		},
		{
			name:        "unlocked GA opt-in stays a silent no-op",
			value:       "TestGAUnlockedFeature=true",
			wantEnabled: map[featuregate.Feature]bool{testGAUnlocked: true},
		},
		{
			name:        "locked GA pair dropped beside an honored unlocked one",
			value:       "TestGAFeature=false,TestGAUnlockedFeature=false",
			wantEnabled: map[featuregate.Feature]bool{testGA: true, testGAUnlocked: false},
			wantWarning: "TestGAFeature=false",
		},
		{
			name:        "deprecated gate stays settable",
			value:       "TestDeprecatedFeature=true",
			wantEnabled: map[featuregate.Feature]bool{testDeprecated: true},
		},
		{
			name:        "last write wins on a duplicate",
			value:       "TestAlphaFeature=true,TestAlphaFeature=false",
			wantEnabled: map[featuregate.Feature]bool{testAlpha: false},
		},
		{
			name:        "group gate enables every alpha gate",
			value:       "AllAlpha=true",
			wantEnabled: map[featuregate.Feature]bool{testAlpha: true, testBeta: true},
		},
		{
			name:        "group gate skips GA gates entirely",
			value:       "TestGAUnlockedFeature=false,AllBeta=true",
			wantEnabled: map[featuregate.Feature]bool{testBeta: true, testGAUnlocked: false},
		},
		{
			name:    "unknown name aborts startup",
			value:   "TestAlphaFeture=true",
			wantErr: "parse --feature-gates: unrecognized feature gate: TestAlphaFeture",
		},
		{
			name:    "non-bool value aborts startup",
			value:   "TestAlphaFeature=maybe",
			wantErr: "parse --feature-gates:",
		},
		{
			name:    "missing value aborts startup",
			value:   "TestAlphaFeature",
			wantErr: "parse --feature-gates: missing bool value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, warnings := newTestRegistry(t)

			err := r.set(tt.value)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("set(%q) = nil, want error containing %q", tt.value, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("set(%q) error = %q, want it to contain %q", tt.value, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("set(%q) = %v, want nil", tt.value, err)
			}

			for gate, want := range tt.wantEnabled {
				if got := r.gate.Enabled(gate); got != want {
					t.Errorf("Enabled(%s) = %t, want %t", gate, got, want)
				}
			}

			joined := strings.Join(*warnings, "\n")
			switch {
			case tt.wantWarning == "" && joined != "":
				t.Errorf("unexpected warning(s): %s", joined)
			case tt.wantWarning != "" && !strings.Contains(joined, tt.wantWarning):
				t.Errorf("warnings = %q, want one naming %q", joined, tt.wantWarning)
			}
		})
	}
}

func TestSummary(t *testing.T) {
	r, _ := newTestRegistry(t)

	if err := r.set("TestAlphaFeature=true,TestGAFeature=false,TestGAUnlockedFeature=false"); err != nil {
		t.Fatalf("set: %v", err)
	}

	// Sorted by name, every known gate present - including the GA ones the
	// help text omits - with the value that actually applies: the locked
	// gate's opt-out was dropped, the unlocked gate's opt-out took.
	want := "AllAlpha=false AllBeta=false TestAlphaFeature=true " +
		"TestBetaFeature=true TestDeprecatedFeature=false TestGAFeature=true " +
		"TestGAUnlockedFeature=false"
	if got := r.summary(); got != want {
		t.Errorf("summary() = %q, want %q", got, want)
	}
}

// TestKnownFeatures pins the help-text inventory rule: every settable gate is
// listed exactly once, the locked GA gate stays hidden, and the unlocked GA
// gate - which the library would hide too - is appended with its stage named.
func TestKnownFeatures(t *testing.T) {
	r, _ := newTestRegistry(t)

	counts := map[string]int{}
	for _, known := range r.knownFeatures() {
		name, _, ok := strings.Cut(known, "=")
		if !ok {
			t.Errorf("knownFeatures() entry %q has no = separator", known)
			continue
		}
		counts[name]++
		if name == string(testGAUnlocked) && !strings.Contains(known, "GA") {
			t.Errorf("knownFeatures() lists %q, want the GA stage named", known)
		}
	}

	for name, want := range map[string]int{
		"AllAlpha":             1,
		"AllBeta":              1,
		string(testAlpha):      1,
		string(testBeta):       1,
		string(testGAUnlocked): 1,
		string(testDeprecated): 0,
		string(testGA):         0,
	} {
		if counts[name] != want {
			t.Errorf("knownFeatures() lists %s %d time(s), want %d", name, counts[name], want)
		}
	}

	if got := r.knownFeatures(); !sort.StringsAreSorted(got) {
		t.Errorf("knownFeatures() = %q, want it sorted", got)
	}
}

func TestShippedRegistry(t *testing.T) {
	// Every project gate registers here with its stage, so an unset
	// --feature-gates resolves to exactly these defaults on top of the
	// library's group gates.
	wantGates := map[featuregate.Feature]featuregate.FeatureSpec{
		ContainerWorkload:      {Default: false, PreRelease: featuregate.Alpha},
		VirtualMachineWorkload: {Default: true, PreRelease: featuregate.Beta},
	}
	if len(defaultFeatureGates) != len(wantGates) {
		t.Fatalf("defaultFeatureGates has %d gate(s), want %d: update this test with the stage of each", len(defaultFeatureGates), len(wantGates))
	}
	for feature, want := range wantGates {
		if got, ok := defaultFeatureGates[feature]; !ok || got != want {
			t.Errorf("defaultFeatureGates[%s] = %+v (present=%t), want %+v", feature, got, ok, want)
		}
	}
	if got, want := Summary(), "AllAlpha=false AllBeta=false ContainerWorkload=false VirtualMachineWorkload=true"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
	knownProject := map[string]int{}
	for _, known := range KnownFeatures() {
		switch {
		case strings.HasPrefix(known, "AllAlpha="), strings.HasPrefix(known, "AllBeta="):
		case strings.HasPrefix(known, "ContainerWorkload="):
			knownProject["ContainerWorkload"]++
			if !strings.Contains(known, "ALPHA") {
				t.Errorf("KnownFeatures() lists %q, want the ALPHA stage named", known)
			}
		case strings.HasPrefix(known, "VirtualMachineWorkload="):
			knownProject["VirtualMachineWorkload"]++
			if !strings.Contains(known, "BETA") {
				t.Errorf("KnownFeatures() lists %q, want the BETA stage named", known)
			}
		default:
			t.Errorf("KnownFeatures() lists %q, want only the group and project gates", known)
		}
	}
	for _, name := range []string{"ContainerWorkload", "VirtualMachineWorkload"} {
		if knownProject[name] != 1 {
			t.Errorf("KnownFeatures() lists %s %d times, want 1", name, knownProject[name])
		}
	}
}
