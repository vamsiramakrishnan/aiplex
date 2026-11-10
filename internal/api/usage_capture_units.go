package api

import "github.com/vamsiramakrishnan/aiplex/pkg/usageledger"

// Observation dimension keys the ledger records (see
// execution.Observation.Dimensions).
const (
	usageDimensionBatch        = "batch"
	usageDimensionImageSize    = "image_size"
	usageDimensionImageQuality = "image_quality"
	usageDimensionServiceTier  = "service_tier"
)

// dimensions copies provider-reported billing dimensions onto the record.
// Values are bounded printable text or dropped; a later conflicting value
// clears the dimension rather than keeping a guess.
func (c *usageCapture) dimensions(values map[string]string) {
	if c == nil || len(values) == 0 {
		return
	}
	c.set(func(r *usageledger.Record) {
		if values[usageDimensionBatch] == "true" {
			r.Batch = true
		}
		for key, target := range map[string]*string{usageDimensionImageSize: &r.ImageSize, usageDimensionImageQuality: &r.ImageQuality, usageDimensionServiceTier: &r.ServiceTier} {
			value, ok := values[key]
			if !ok || value == "" || len(value) > 64 || !quotaText(value, true) {
				continue
			}
			if *target != "" && *target != value {
				*target = "mixed"
				continue
			}
			*target = value
		}
	})
}

// markBatch labels the exchange as part of a provider batch job.
func (c *usageCapture) markBatch() {
	c.set(func(r *usageledger.Record) { r.Batch = true })
}
