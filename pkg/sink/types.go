package sink

import "context"

type OverflowPolicy string

const (
	OverflowDrop  OverflowPolicy = "drop"
	OverflowBlock OverflowPolicy = "block"
)

type Transport interface {
	Consume(batch []byte) error
	Close(ctx context.Context) error
}
