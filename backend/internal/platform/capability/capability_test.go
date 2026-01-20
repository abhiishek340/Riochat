package capability

import "testing"

func TestServerAndLocalSets(t *testing.T) {
	for _, n := range Names() {
		if !Server().Enabled(n) {
			t.Errorf("server must enable %s", n)
		}
	}
	local := Local()
	if got := local.Sorted(); len(got) != 1 || got[0] != UsageMetering {
		t.Fatalf("local must enable only usageMetering: %v", got)
	}
	if len(Names()) != 10 {
		t.Fatalf("expected ten flags, got %d", len(Names()))
	}
}

func TestUnknownIsFalseAndMustBeKnownPanics(t *testing.T) {
	if Server().Enabled("multiuser") { // wrong case on purpose
		t.Fatal("misspelled key must never be enabled")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("MustBeKnown must panic for unknown flags")
		}
	}()
	MustBeKnown("sharng")
}

func TestFeaturesPayloadHasAllKeys(t *testing.T) {
	f := Local().Features()
	if len(f) != 10 || !f[UsageMetering] || f[Sharing] {
		t.Fatalf("unexpected payload: %v", f)
	}
}
