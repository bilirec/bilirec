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
		parsed.OnlyAudio != original.OnlyAudio {
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
}
