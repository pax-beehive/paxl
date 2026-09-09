package adaptor

import (
	"encoding/json"
	"fmt"
	"math"
)

// Released v0/v1 chunks have a sequence range instead of seq/time. The settled
// assistant/message owns the transcript, so chunks only advance the watermark.
func normalizeDSHPackedEvent(event *dshEvent, version int) error {
	switch event.Type {
	case "text-chunks", "reasoning-chunks", "tool-call-chunks":
	default:
		return nil
	}
	if version >= 2 {
		return fmt.Errorf("packed DSH row in v2 log")
	}
	var row struct {
		Seq  *int64 `json:"seq0"`
		Time *int64 `json:"time0"`
		Data struct {
			Texts []string `json:"texts"`
			Args  []string `json:"args"`
			DT    []int64  `json:"dt"`
		} `json:"data"`
	}
	if json.Unmarshal(event.Raw, &row) != nil || row.Seq == nil || row.Time == nil {
		return fmt.Errorf("invalid DSH packed row")
	}
	count := len(row.Data.Texts)
	if event.Type == "tool-call-chunks" {
		count = len(row.Data.Args)
	}
	if count == 0 || len(row.Data.DT) != count-1 || *row.Seq < 0 || *row.Time < 0 ||
		*row.Seq > math.MaxInt64-int64(count) {
		return fmt.Errorf("invalid DSH packed range")
	}
	event.Seq, event.Time = *row.Seq+int64(count)-1, *row.Time
	for _, gap := range row.Data.DT {
		if gap < 0 || event.Time > math.MaxInt64-gap {
			return fmt.Errorf("invalid DSH packed timestamp")
		}
		event.Time += gap
	}
	event.Type = "assistant/chunk"
	return nil
}
