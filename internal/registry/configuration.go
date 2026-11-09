package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vamsiramakrishnan/aiplex/internal/models"
)

var ErrConfigurationConflict = errors.New("configuration revision conflict")

const (
	ConfigAgent       = "agent"
	ConfigRoute       = "route"
	ConfigProvider    = "provider"
	ConfigRoleBinding = "role-binding"
	ConfigUserScopes  = "user-scopes"
)

// ConfigurationRecord is a revisioned view of an existing legacy configuration.
// Deletion retains a revision tombstone; recreate uses expected=0 and never reuses
// an earlier revision. These records deliberately retain their original tables.
type ConfigurationRecord struct {
	Kind     string          `json:"kind"`
	ID       string          `json:"id"`
	Revision int64           `json:"revision"`
	Spec     json.RawMessage `json:"spec"`
}

// VersionedConfigurationStore supplies atomic desired-state updates. Generation
// fences dependency reads: read it before validating related configurations and
// pass it to the write. Use -1 only for operator-owned unconditional bootstraps.
// This contract does not claim atomicity of subsequent external cloud writes.
type VersionedConfigurationStore interface {
	ConfigurationGeneration(context.Context) (int64, error)
	GetConfiguration(context.Context, string, string) (ConfigurationRecord, error)
	PutConfiguration(context.Context, ConfigurationRecord, int64, int64) (ConfigurationRecord, error)
	DeleteConfiguration(context.Context, string, string, int64, int64) error
	ModifyUserScopes(context.Context, string, func([]string) ([]string, error)) error
}

type configurationRevision struct {
	Revision int64
	Deleted  bool
}
type configurationScopes struct {
	Scopes   []string `json:"scopes" firestore:"scopes"`
	Revision int64    `json:"revision"`
}

func configurationKey(kind, id string) string { return kind + ":" + id }
func configurationIdentity(kind, id string) error {
	switch kind {
	case ConfigAgent, ConfigRoute, ConfigProvider, ConfigRoleBinding, ConfigUserScopes:
	default:
		return fmt.Errorf("unknown configuration kind")
	}
	if id == "" || len(id) > 512 || !utf8.ValidString(id) || strings.ContainsAny(id, "/\\\x00\r\n") {
		return fmt.Errorf("bounded configuration identifier required")
	}
	for _, ch := range id {
		if ch < 0x20 || ch == 0x7f {
			return fmt.Errorf("configuration identifier contains control characters")
		}
	}
	return nil
}
func configurationValue(kind string) any {
	switch kind {
	case ConfigAgent:
		return &models.Agent{}
	case ConfigRoute:
		return &models.LLMRouteConfig{}
	case ConfigProvider:
		return &models.ProviderConfig{}
	case ConfigRoleBinding:
		return &models.RoleBinding{}
	case ConfigUserScopes:
		return &configurationScopes{}
	}
	return nil
}
func configurationValueID(value any) string {
	switch v := value.(type) {
	case *models.Agent:
		return v.ClientID
	case *models.LLMRouteConfig:
		return v.ModelID
	case *models.ProviderConfig:
		return v.Provider
	case *models.RoleBinding:
		return v.ID
	}
	return ""
}
func configurationVersion(value any) int64 {
	switch v := value.(type) {
	case *models.Agent:
		return v.Revision
	case *models.LLMRouteConfig:
		return v.Revision
	case *models.ProviderConfig:
		return v.Revision
	case *models.RoleBinding:
		return v.Revision
	case *configurationScopes:
		return v.Revision
	}
	return 0
}
func setConfigurationVersion(value any, rev int64) {
	switch v := value.(type) {
	case *models.Agent:
		v.Revision = rev
		v.ResourceVersion = rev
		v.ClientSecret = ""
	case *models.LLMRouteConfig:
		v.Revision = rev
	case *models.ProviderConfig:
		v.Revision = rev
	case *models.RoleBinding:
		v.Revision = rev
	case *configurationScopes:
		v.Revision = rev
	}
}
func normalizeConfigurationVersion(value any) {
	if configurationVersion(value) == 0 {
		setConfigurationVersion(value, 1)
	}
}
func decodeConfiguration(record ConfigurationRecord, revision int64) (any, error) {
	if err := configurationIdentity(record.Kind, record.ID); err != nil {
		return nil, err
	}
	if len(record.Spec) > 1<<20 || !json.Valid(record.Spec) || string(record.Spec) == "null" {
		return nil, fmt.Errorf("bounded configuration object required")
	}
	value := configurationValue(record.Kind)
	if err := json.Unmarshal(record.Spec, value); err != nil {
		return nil, err
	}
	if record.Kind != ConfigUserScopes && configurationValueID(value) != record.ID {
		return nil, fmt.Errorf("configuration identity disagrees with payload")
	}
	setConfigurationVersion(value, revision)
	return value, nil
}
func configurationRecord(kind, id string, value any) (ConfigurationRecord, error) {
	normalizeConfigurationVersion(value)
	raw, err := json.Marshal(value)
	return ConfigurationRecord{Kind: kind, ID: id, Revision: configurationVersion(value), Spec: raw}, err
}
func validateConfigurationWrite(expected, generation int64) error {
	if expected < 0 || generation < -1 {
		return fmt.Errorf("invalid configuration precondition")
	}
	return nil
}
func nextConfigurationRevision(current int64) (int64, error) {
	if current < 0 || current >= math.MaxInt64 {
		return 0, ErrConfigurationConflict
	}
	return current + 1, nil
}
func (m *MemoryStore) configurationLocked(kind, id string) (ConfigurationRecord, error) {
	var value any
	switch kind {
	case ConfigAgent:
		if v := m.agents[id]; v != nil {
			value = cloneAuthAgent(v)
		}
	case ConfigRoute:
		if v := m.routeConfigs[id]; v != nil {
			value = memorySnapshot(v)
		}
	case ConfigProvider:
		if v := m.providerConfigs[id]; v != nil {
			value = memorySnapshot(v)
		}
	case ConfigRoleBinding:
		if v := m.roleBindings[id]; v != nil {
			value = cloneAuthRoleBinding(v)
		}
	case ConfigUserScopes:
		if v, ok := m.userScopes[id]; ok {
			value = &configurationScopes{Scopes: cloneAuthScopes(v)}
		}
	}
	if value == nil {
		return ConfigurationRecord{}, ErrNotFound
	}
	if revision, ok := m.configurationRevisions[configurationKey(kind, id)]; ok {
		if revision.Deleted {
			return ConfigurationRecord{}, ErrConfigurationConflict
		}
		setConfigurationVersion(value, revision.Revision)
	}
	return configurationRecord(kind, id, value)
}
func (m *MemoryStore) ConfigurationGeneration(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.configurationGeneration, nil
}
func (m *MemoryStore) GetConfiguration(ctx context.Context, kind, id string) (ConfigurationRecord, error) {
	if err := ctx.Err(); err != nil {
		return ConfigurationRecord{}, err
	}
	if err := configurationIdentity(kind, id); err != nil {
		return ConfigurationRecord{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.configurationLocked(kind, id)
}
func (m *MemoryStore) putConfigurationLocked(record ConfigurationRecord, expected, generation int64) (ConfigurationRecord, error) {
	if err := validateConfigurationWrite(expected, generation); err != nil {
		return ConfigurationRecord{}, err
	}
	if generation >= 0 && generation != m.configurationGeneration {
		return ConfigurationRecord{}, ErrConfigurationConflict
	}
	current, err := m.configurationLocked(record.Kind, record.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return ConfigurationRecord{}, err
	}
	if current.Revision != expected {
		return ConfigurationRecord{}, ErrConfigurationConflict
	}
	last := current.Revision
	if tombstone, ok := m.configurationRevisions[configurationKey(record.Kind, record.ID)]; ok && tombstone.Revision > last {
		last = tombstone.Revision
	}
	next, err := nextConfigurationRevision(last)
	if err != nil {
		return ConfigurationRecord{}, err
	}
	if m.configurationGeneration == math.MaxInt64 {
		return ConfigurationRecord{}, ErrConfigurationConflict
	}
	value, err := decodeConfiguration(record, next)
	if err != nil {
		return ConfigurationRecord{}, err
	}
	switch v := value.(type) {
	case *models.Agent:
		m.agents[record.ID] = v
	case *models.LLMRouteConfig:
		v.UpdatedAt = time.Now()
		if v.CreatedAt.IsZero() {
			v.CreatedAt = v.UpdatedAt
		}
		m.routeConfigs[record.ID] = v
	case *models.ProviderConfig:
		m.providerConfigs[record.ID] = v
	case *models.RoleBinding:
		m.roleBindings[record.ID] = v
	case *configurationScopes:
		m.userScopes[record.ID] = cloneAuthScopes(v.Scopes)
	}
	if m.configurationRevisions == nil {
		m.configurationRevisions = map[string]configurationRevision{}
	}
	m.configurationRevisions[configurationKey(record.Kind, record.ID)] = configurationRevision{Revision: next}
	m.configurationGeneration++
	return configurationRecord(record.Kind, record.ID, value)
}
func (m *MemoryStore) PutConfiguration(ctx context.Context, record ConfigurationRecord, expected, generation int64) (ConfigurationRecord, error) {
	if err := ctx.Err(); err != nil {
		return ConfigurationRecord{}, err
	}
	if err := configurationIdentity(record.Kind, record.ID); err != nil {
		return ConfigurationRecord{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.putConfigurationLocked(record, expected, generation)
}
func (m *MemoryStore) DeleteConfiguration(ctx context.Context, kind, id string, expected, generation int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := configurationIdentity(kind, id); err != nil {
		return err
	}
	if err := validateConfigurationWrite(expected, generation); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.configurationLocked(kind, id)
	if err != nil {
		return err
	}
	if expected == 0 || current.Revision != expected || (generation >= 0 && generation != m.configurationGeneration) {
		return ErrConfigurationConflict
	}
	next, err := nextConfigurationRevision(current.Revision)
	if err != nil {
		return err
	}
	if m.configurationGeneration == math.MaxInt64 {
		return ErrConfigurationConflict
	}
	switch kind {
	case ConfigAgent:
		delete(m.agents, id)
	case ConfigRoute:
		delete(m.routeConfigs, id)
	case ConfigProvider:
		delete(m.providerConfigs, id)
	case ConfigRoleBinding:
		delete(m.roleBindings, id)
	case ConfigUserScopes:
		delete(m.userScopes, id)
	}
	if m.configurationRevisions == nil {
		m.configurationRevisions = map[string]configurationRevision{}
	}
	m.configurationRevisions[configurationKey(kind, id)] = configurationRevision{Revision: next, Deleted: true}
	m.configurationGeneration++
	return nil
}
func (m *MemoryStore) ModifyUserScopes(ctx context.Context, id string, modify func([]string) ([]string, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := configurationIdentity(ConfigUserScopes, id); err != nil {
		return err
	}
	if modify == nil {
		return fmt.Errorf("scope mutation required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, err := m.configurationLocked(ConfigUserScopes, id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	scopes, err := modify(cloneAuthScopes(m.userScopes[id]))
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(configurationScopes{Scopes: scopes})
	_, err = m.putConfigurationLocked(ConfigurationRecord{Kind: ConfigUserScopes, ID: id, Spec: raw}, existing.Revision, -1)
	return err
}

// PutLegacyConfiguration is for trusted bootstrap/reconciliation code. HTTP
// handlers must call PutConfiguration with explicit caller preconditions.
func PutLegacyConfiguration(ctx context.Context, store VersionedConfigurationStore, kind, id string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	for attempts := 0; attempts < 8; attempts++ {
		current, err := store.GetConfiguration(ctx, kind, id)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		written, err := store.PutConfiguration(ctx, ConfigurationRecord{Kind: kind, ID: id, Spec: raw}, current.Revision, -1)
		if errors.Is(err, ErrConfigurationConflict) {
			continue
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(written.Spec, value)
	}
	return ErrConfigurationConflict
}
func DeleteLegacyConfiguration(ctx context.Context, store VersionedConfigurationStore, kind, id string) error {
	current, err := store.GetConfiguration(ctx, kind, id)
	if err != nil {
		return err
	}
	return store.DeleteConfiguration(ctx, kind, id, current.Revision, -1)
}

var _ VersionedConfigurationStore = (*MemoryStore)(nil)
