package processors_test

import (
	"context"
	"testing"

	"github.com/bilirec/bilirec/pkg/logger"

	"github.com/bilirec/bilirec/internal/processors"
)

const (
	tsPacketSize = 188
	tsSyncByte   = 0x47
)

func makeTSPacket(pid uint16, adaptationFieldControl uint8, cc uint8, discontinuity bool) []byte {
	pkt := make([]byte, tsPacketSize)
	pkt[0] = tsSyncByte
	pkt[1] = byte((pid >> 8) & 0x1F)
	pkt[2] = byte(pid & 0xFF)
	pkt[3] = ((adaptationFieldControl & 0x3) << 4) | (cc & 0x0F)

	if adaptationFieldControl == 0x3 {
		pkt[4] = 1 // adaptation field length
		if discontinuity {
			pkt[5] = 0x80
		}
	}

	return pkt
}

func continuityCounter(pkt []byte) uint8 {
	return pkt[3] & 0x0F
}

func TestTsContinuityFixer_SkipReservedAdaptationFieldControlZero(t *testing.T) {
	proc := &processors.TsContinuityFixerProcessor{}
	if err := proc.Open(context.Background(), logger.Nop()); err != nil {
		t.Fatalf("open processor: %v", err)
	}
	defer proc.Close()

	log := logger.Nop()
	pid := uint16(256)

	pkt1 := makeTSPacket(pid, 0x1, 5, false)
	if _, err := proc.Process(context.Background(), log, pkt1); err != nil {
		t.Fatalf("process pkt1: %v", err)
	}

	// Reserved/invalid adaptation_field_control=0 must be ignored and never patched.
	pkt2 := makeTSPacket(pid, 0x0, 2, false)
	if _, err := proc.Process(context.Background(), log, pkt2); err != nil {
		t.Fatalf("process pkt2: %v", err)
	}
	if got := continuityCounter(pkt2); got != 2 {
		t.Fatalf("reserved afc=0 packet should not be patched: got CC=%d want=2", got)
	}

	pkt3 := makeTSPacket(pid, 0x1, 9, false)
	if _, err := proc.Process(context.Background(), log, pkt3); err != nil {
		t.Fatalf("process pkt3: %v", err)
	}
	if got := continuityCounter(pkt3); got != 6 {
		t.Fatalf("unexpected CC after afc=0 skip: got=%d want=6", got)
	}
}
