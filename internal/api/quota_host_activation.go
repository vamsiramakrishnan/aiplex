package api

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/vamsiramakrishnan/aiplex/pkg/quotaplex"
)

const quotaHostConfigurationKind = "quota-host-config"

type quotaHostConfiguration struct {
	Policy              quotaplex.PolicyRef `json:"policy"`
	ConfigurationDigest string              `json:"configuration_digest"`
	ConfiguredAt        time.Time           `json:"configured_at"`
}

func quotaCanonicalDigest(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == 32 && hex.EncodeToString(data) == value
}

func (h *QuotaHost) configureHeads(ctx context.Context, cfg QuotaHostConfig) error {
	authorityDigest := ""
	needsBounds := false
	for _, binding := range h.bindings {
		if quotaStrictProfile(binding.Profile) && binding.Profile != quotaProfileLiveAllowance {
			needsBounds = true
		}
	}
	if needsBounds {
		authority, ok := h.bounds.(interface{ ConfigurationDigest() string })
		if !ok {
			return fmt.Errorf("trusted token bound authority requires an immutable configuration digest")
		}
		authorityDigest = authority.ConfigurationDigest()
		if !quotaCanonicalDigest(authorityDigest) {
			return fmt.Errorf("trusted token bound authority configuration digest is invalid")
		}
	}
	previous := map[string]*quotaHostConfiguration{}
	for _, tenant := range cfg.Tenants {
		id := tenant.Policy.TenantID
		bindings := []QuotaBinding{}
		for _, binding := range h.bindings {
			if binding.TenantID == id {
				bindings = append(bindings, binding)
			}
		}
		sort.Slice(bindings, func(i, j int) bool { return bindings[i].ID < bindings[j].ID })
		membershipAuthority := ""
		if h.membership != nil {
			membershipAuthority = h.membership.ConfigurationDigest()
		}
		admissionController := ""
		if h.admission != nil {
			admissionController = h.admission.ConfigurationDigest()
		}
		digest, err := quotaDigest(struct {
			Bindings            []QuotaBinding
			MembershipAge       time.Duration
			BoundsAuthority     string
			MembershipAuthority string `json:",omitempty"`
			AdmissionController string `json:",omitempty"`
		}{bindings, h.maxMembershipAge, authorityDigest, membershipAuthority, admissionController})
		if err != nil {
			return err
		}
		h.configurationDigests[id] = digest
		reference := h.tenants[id].policy.Reference()
		err = h.store.View(ctx, id, func(tx quotaplex.Tx) error {
			var head quotaHostConfiguration
			err := tx.Get(quotaHostConfigurationKind, "active", &head)
			if errors.Is(err, quotaplex.ErrNotFound) {
				if !tenant.Activate {
					return fmt.Errorf("explicit quota host activation is required")
				}
				previous[id] = nil
				return nil
			}
			if err != nil {
				return err
			}
			if !quotaCanonicalDigest(head.ConfigurationDigest) || head.Policy.ID == "" || head.Policy.Revision == 0 || !quotaCanonicalDigest(head.Policy.Digest) {
				return fmt.Errorf("persisted quota host head is invalid")
			}
			if head.Policy == reference {
				if head.ConfigurationDigest != digest {
					return fmt.Errorf("quota binding changes require a new policy revision: %w", quotaplex.ErrConflict)
				}
			} else if !tenant.Activate || head.Policy.Digest != tenant.ExpectedDigest || head.Policy.ID != reference.ID || reference.Revision <= head.Policy.Revision {
				return fmt.Errorf("quota host activation revision changed: %w", quotaplex.ErrConflict)
			}
			previous[id] = &head
			return nil
		})
		if err != nil {
			return err
		}
	}
	for _, tenant := range cfg.Tenants {
		id := tenant.Policy.TenantID
		value := h.tenants[id]
		var err error
		if tenant.Activate {
			err = value.engine.Activate(ctx, tenant.ExpectedDigest)
		} else {
			err = value.engine.Ready(ctx)
		}
		if err != nil {
			return err
		}
		wanted := quotaHostConfiguration{Policy: value.policy.Reference(), ConfigurationDigest: h.configurationDigests[id], ConfiguredAt: h.clock().UTC()}
		if wanted.ConfiguredAt.IsZero() {
			return fmt.Errorf("quota host clock is invalid")
		}
		err = h.store.Update(ctx, id, func(tx quotaplex.Tx) error {
			var current quotaHostConfiguration
			err := tx.Get(quotaHostConfigurationKind, "active", &current)
			if err == nil && current.Policy == wanted.Policy && current.ConfigurationDigest == wanted.ConfigurationDigest {
				return nil
			}
			old := previous[id]
			if old == nil {
				if !errors.Is(err, quotaplex.ErrNotFound) {
					if err != nil {
						return err
					}
					return quotaplex.ErrConflict
				}
				return tx.Create(quotaHostConfigurationKind, "active", wanted)
			}
			if err != nil {
				return err
			}
			if current.Policy != old.Policy || current.ConfigurationDigest != old.ConfigurationDigest {
				return quotaplex.ErrConflict
			}
			return tx.Put(quotaHostConfigurationKind, "active", wanted)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (h *QuotaHost) assertActive(ctx context.Context, tenant string) error {
	value, ok := h.tenants[tenant]
	if !ok {
		return fmt.Errorf("quota tenant is unavailable")
	}
	if err := value.engine.Ready(ctx); err != nil {
		return err
	}
	return h.store.View(ctx, tenant, func(tx quotaplex.Tx) error {
		var head quotaHostConfiguration
		if err := tx.Get(quotaHostConfigurationKind, "active", &head); err != nil {
			return err
		}
		if head.Policy != value.policy.Reference() || head.ConfigurationDigest != h.configurationDigests[tenant] {
			return fmt.Errorf("quota host binding revision changed: %w", quotaplex.ErrConflict)
		}
		return nil
	})
}
