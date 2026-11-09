package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

const quotaBindingQuarantineKind = "quota-binding-quarantine"

type quotaBindingQuarantine struct {
	BindingID, PolicyDigest, ConfigurationDigest, ObservedModelVersion string
	Reason                                                             string
	At                                                                 time.Time
}

func (h *QuotaHost) quarantineKey(tenant, bindingID string) string {
	key, _ := quotaDigest([]string{h.tenants[tenant].policy.Reference().Digest, h.configurationDigests[tenant], bindingID})
	return key
}

func (h *QuotaHost) quarantineBinding(ctx context.Context, binding QuotaBinding, modelVersion, reason string) error {
	key := h.quarantineKey(binding.TenantID, binding.ID)
	value := quotaBindingQuarantine{BindingID: binding.ID, PolicyDigest: h.tenants[binding.TenantID].policy.Reference().Digest, ConfigurationDigest: h.configurationDigests[binding.TenantID], ObservedModelVersion: modelVersion, Reason: reason, At: h.clock().UTC()}
	return h.store.Update(ctx, binding.TenantID, func(tx quotaplex.Tx) error {
		var old quotaBindingQuarantine
		if err := tx.Get(quotaBindingQuarantineKind, key, &old); err == nil {
			return nil
		} else if !errors.Is(err, quotaplex.ErrNotFound) {
			return err
		}
		return tx.Create(quotaBindingQuarantineKind, key, value)
	})
}

func (h *QuotaHost) assertBindingActive(ctx context.Context, tenant, bindingID string) error {
	value, ok := h.tenants[tenant]
	if !ok {
		return fmt.Errorf("quota tenant unavailable")
	}
	if err := value.engine.Ready(ctx); err != nil {
		return err
	}
	return h.store.View(ctx, tenant, func(tx quotaplex.Tx) error {
		key := h.quarantineKey(tenant, bindingID)
		if err := tx.Prefetch([]quotaplex.Key{{Kind: quotaHostConfigurationKind, ID: "active"}, {Kind: quotaBindingQuarantineKind, ID: key}}); err != nil {
			return err
		}
		var head quotaHostConfiguration
		if err := tx.Get(quotaHostConfigurationKind, "active", &head); err != nil {
			return err
		}
		if head.Policy != value.policy.Reference() || head.ConfigurationDigest != h.configurationDigests[tenant] {
			return quotaplex.ErrConflict
		}
		var quarantine quotaBindingQuarantine
		if err := tx.Get(quotaBindingQuarantineKind, key, &quarantine); errors.Is(err, quotaplex.ErrNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		return fmt.Errorf("quota binding generation is quarantined")
	})
}
