package subscribe

import (
	"testing"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
)

func TestRoomConfigRoundTrip(t *testing.T) {
	original := &RoomConfig{
		AutoRecord:            true,
		Notify:                true,
		RecordDurationMinutes: 180,
		Qn:                    int(bilibili.QualityHigh),
		OnlyAudio:             true,
		StreamProfiles:        []string{string(bilibili.ProfileHLSFMP4), string(bilibili.ProfileHLSTS)},
	}

	data, err := roomConfigSerializer.Serialize(original)
	if err != nil {
		t.Fatalf("serialize failed: %v", err)
	}

	parsed, err := parseRoomConfig(data)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if parsed.AutoRecord != original.AutoRecord ||
		parsed.Notify != original.Notify ||
		parsed.RecordDurationMinutes != original.RecordDurationMinutes ||
		parsed.Qn != original.Qn ||
		parsed.OnlyAudio != original.OnlyAudio ||
		len(parsed.StreamProfiles) != len(original.StreamProfiles) ||
		parsed.StreamProfiles[0] != original.StreamProfiles[0] ||
		parsed.StreamProfiles[1] != original.StreamProfiles[1] {
		t.Fatalf("round trip mismatch: got %+v want %+v", parsed, original)
	}
}

func TestParseRoomConfigDefaultsNewFields(t *testing.T) {
	legacy := mustSerializeRoomConfig(&RoomConfig{
		AutoRecord:            true,
		Notify:                false,
		RecordDurationMinutes: 60,
	})

	parsed, err := parseRoomConfig(legacy)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if parsed.Qn != 0 {
		t.Fatalf("expected default qn 0, got %d", parsed.Qn)
	}
	if parsed.OnlyAudio {
		t.Fatal("expected default onlyAudio false")
	}
	if len(parsed.StreamProfiles) != 0 {
		t.Fatalf("expected default stream_profiles empty, got %v", parsed.StreamProfiles)
	}
}
