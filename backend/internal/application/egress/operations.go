package egress

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	domain "github.com/chenyme/grok2api/backend/internal/domain/egress"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

const (
	defaultProbeIntervalSeconds      = 900
	defaultAssignmentIntervalSeconds = 300
	maxEgressAccountCapacity         = 100000
	maxManualProbeNodes              = 200
	maxConcurrentProbes              = 8
)

var ErrOperationsUnavailable = errors.New("Fungsi operasi proksi tidak tersedia")

// OperationsRepository is deliberately optional. Existing egress consumers
// still only need the narrow routing repository while relational persistence
// provides this richer administrative surface.
type OperationsRepository interface {
	ListEgressSources(context.Context) ([]domain.SubscriptionSource, error)
	ListEgressSourcePage(context.Context, repository.EgressSourceListQuery) ([]domain.SubscriptionSource, int64, error)
	ListDueEgressSources(context.Context, time.Time, int) ([]domain.SubscriptionSource, error)
	GetEgressSource(context.Context, uint64) (domain.SubscriptionSource, error)
	CreateEgressSource(context.Context, domain.SubscriptionSource) (domain.SubscriptionSource, error)
	UpdateEgressSource(context.Context, domain.SubscriptionSource) (domain.SubscriptionSource, error)
	DeleteEgressSource(context.Context, uint64) error
	UpdateEgressSourceSync(context.Context, uint64, time.Time, time.Time, int, string) error
	UpsertEgressNodesFromSource(context.Context, uint64, []domain.Node) (int, error)
	CreateEgressNodes(context.Context, []domain.Node) (int, error)
	UpdateEgressNodeProbe(context.Context, uint64, string, domain.ProbeResult) error
	ListDueEgressNodes(context.Context, time.Time, time.Duration, int) ([]domain.Node, error)
	GetEgressOperationsConfig(context.Context) (domain.OperationsConfig, error)
	SaveEgressOperationsConfig(context.Context, domain.OperationsConfig) (domain.OperationsConfig, error)
}

// NodeProber is implemented by the infrastructure egress manager. Its fixed
// probe endpoint prevents admin input from controlling the outbound target.
type NodeProber interface {
	ProbeEgressNode(context.Context, domain.Node) (domain.ProbeResult, error)
}

type OperationsConfigInvalidator interface {
	InvalidateOperationsConfig()
}

type SubscriptionSourceInput struct {
	Name                   string
	Scope                  domain.Scope
	Enabled                bool
	URL                    *string
	ClearURL               bool
	ProxyURL               *string
	ClearProxyURL          bool
	RefreshIntervalSeconds *int
	DefaultAccountCapacity *int
}

type SourceListFilter struct {
	Scope domain.Scope
}

type ImportInput struct {
	Name            string
	Scope           domain.Scope
	AccountCapacity int
	Content         string
}

type ImportResult struct {
	Imported int
	Skipped  int
}

type ProbeBatchResult struct {
	Requested int
	Healthy   int
	Unhealthy int
}

type OperationsConfigInput struct {
	ProbeProvider             domain.ProbeProvider
	ProbeIntervalSeconds      int
	AutoAssignEnabled         bool
	AutoBalanceEnabled        bool
	AssignmentIntervalSeconds int
	Fallbacks                 map[domain.Scope]FallbackConfigInput
}

type FallbackConfigInput struct {
	Mode   domain.FallbackMode
	NodeID uint64
}

func (s *Service) operationsRepository() (OperationsRepository, error) {
	if s == nil || s.operations == nil {
		return nil, ErrOperationsUnavailable
	}
	return s.operations, nil
}

func (s *Service) SetNodeProber(value NodeProber) {
	s.mu.Lock()
	s.prober = value
	s.mu.Unlock()
}

func (s *Service) SetOperationsConfigInvalidator(value OperationsConfigInvalidator) {
	s.mu.Lock()
	s.operationsCache = value
	s.mu.Unlock()
}

func (s *Service) invalidateOperationsConfig() {
	s.mu.RLock()
	value := s.operationsCache
	s.mu.RUnlock()
	if value != nil {
		value.InvalidateOperationsConfig()
	}
}

func (s *Service) nodeProber() NodeProber {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prober
}

func (s *Service) ListSources(ctx context.Context) ([]domain.PublicSubscriptionSource, error) {
	operations, err := s.operationsRepository()
	if err != nil {
		return nil, err
	}
	values, err := operations.ListEgressSources(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.PublicSubscriptionSource, 0, len(values))
	for _, value := range values {
		result = append(result, publicSource(value))
	}
	return result, nil
}

func (s *Service) ListSourcePage(ctx context.Context, page, pageSize int, search string, filter SourceListFilter) ([]domain.PublicSubscriptionSource, int64, error) {
	page, pageSize = repository.NormalizePage(page, pageSize, repository.DefaultPageSize)
	if !validListScope(filter.Scope) {
		return nil, 0, ErrInvalidFilter
	}
	operations, err := s.operationsRepository()
	if err != nil {
		return nil, 0, err
	}
	values, total, err := operations.ListEgressSourcePage(ctx, repository.EgressSourceListQuery{
		Page:   repository.PageQuery{Offset: (page - 1) * pageSize, Limit: pageSize, Search: strings.TrimSpace(search)},
		Filter: repository.EgressSourceListFilter{Scope: filter.Scope},
	})
	if err != nil {
		return nil, 0, err
	}
	result := make([]domain.PublicSubscriptionSource, 0, len(values))
	for _, value := range values {
		result = append(result, publicSource(value))
	}
	return result, total, nil
}

func (s *Service) CreateSource(ctx context.Context, input SubscriptionSourceInput) (domain.PublicSubscriptionSource, error) {
	operations, err := s.operationsRepository()
	if err != nil {
		return domain.PublicSubscriptionSource{}, err
	}
	value, err := s.applySourceInput(domain.SubscriptionSource{}, input, true)
	if err != nil {
		return domain.PublicSubscriptionSource{}, err
	}
	created, err := operations.CreateEgressSource(ctx, value)
	if err != nil {
		return domain.PublicSubscriptionSource{}, err
	}
	return publicSource(created), nil
}

func (s *Service) UpdateSource(ctx context.Context, id uint64, input SubscriptionSourceInput) (domain.PublicSubscriptionSource, error) {
	operations, err := s.operationsRepository()
	if err != nil {
		return domain.PublicSubscriptionSource{}, err
	}
	value, err := operations.GetEgressSource(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.PublicSubscriptionSource{}, ErrNotFound
	}
	if err != nil {
		return domain.PublicSubscriptionSource{}, err
	}
	previousScope := value.Scope
	value, err = s.applySourceInput(value, input, false)
	if err != nil {
		return domain.PublicSubscriptionSource{}, err
	}
	if previousScope != value.Scope {
		if err := s.validateSourceBindingScope(ctx, value.ID, value.Scope); err != nil {
			return domain.PublicSubscriptionSource{}, err
		}
	}
	updated, err := operations.UpdateEgressSource(ctx, value)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.PublicSubscriptionSource{}, ErrNotFound
	}
	if err != nil {
		return domain.PublicSubscriptionSource{}, err
	}
	return publicSource(updated), nil
}

func (s *Service) DeleteSource(ctx context.Context, id uint64) error {
	operations, err := s.operationsRepository()
	if err != nil {
		return err
	}
	err = operations.DeleteEgressSource(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

func (s *Service) SyncSource(ctx context.Context, id uint64) (ImportResult, error) {
	operations, err := s.operationsRepository()
	if err != nil {
		return ImportResult{}, err
	}
	source, err := operations.GetEgressSource(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return ImportResult{}, ErrNotFound
	}
	if err != nil {
		return ImportResult{}, err
	}
	return s.syncSource(ctx, operations, source)
}

func (s *Service) ImportText(ctx context.Context, input ImportInput) (ImportResult, error) {
	operations, err := s.operationsRepository()
	if err != nil {
		return ImportResult{}, err
	}
	if err := validateImportInput(input); err != nil {
		return ImportResult{}, err
	}
	entries, skipped, err := parseProxySubscription(input.Content)
	if err != nil {
		return ImportResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	nodes := make([]domain.Node, 0, len(entries))
	for index, entry := range entries {
		encryptedProxy, encryptErr := s.cipher.Encrypt(entry.ProxyURL)
		if encryptErr != nil {
			return ImportResult{}, encryptErr
		}
		nodes = append(nodes, domain.Node{
			Name: sourceNodeName(input.Name, index), Scope: input.Scope, Enabled: true,
			AccountCapacity: input.AccountCapacity, EncryptedProxyURL: encryptedProxy, Health: 1,
			ProbeStatus: domain.ProbeStatusUnknown,
		})
	}
	created, err := operations.CreateEgressNodes(ctx, nodes)
	if err != nil {
		return ImportResult{}, err
	}
	return ImportResult{Imported: created, Skipped: skipped}, nil
}

func (s *Service) TestNode(ctx context.Context, id uint64) (domain.ProbeResult, error) {
	operations, err := s.operationsRepository()
	if err != nil {
		return domain.ProbeResult{}, err
	}
	node, err := s.repository.GetEgressNode(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.ProbeResult{}, ErrNotFound
	} else if err != nil {
		return domain.ProbeResult{}, err
	}
	prober := s.nodeProber()
	if prober == nil {
		return domain.ProbeResult{}, ErrOperationsUnavailable
	}
	result, probeErr := prober.ProbeEgressNode(ctx, node)
	if result.TestedAt.IsZero() {
		result.TestedAt = time.Now().UTC()
	}
	if !result.Status.IsValid() {
		result.Status = domain.ProbeStatusUnhealthy
	}
	if probeErr != nil {
		result.Status = domain.ProbeStatusUnhealthy
		if strings.TrimSpace(result.Error) == "" {
			result.Error = "Pengesanan proksi gagal"
		}
	}
	if updateErr := operations.UpdateEgressNodeProbe(ctx, id, node.EncryptedProxyURL, result); updateErr != nil {
		if errors.Is(updateErr, repository.ErrNotFound) {
			return result, ErrNotFound
		}
		if errors.Is(updateErr, repository.ErrConflict) {
			return result, ErrProbeStale
		}
		return result, updateErr
	}
	// An unreachable proxy is a completed probe with an unhealthy result, not
	// an API operation failure. Persistence and repository failures still return
	// above so callers can distinguish them from node health.
	return result, nil
}

func (s *Service) TestNodes(ctx context.Context, ids []uint64) (ProbeBatchResult, error) {
	if len(ids) == 0 {
		nodes, err := s.repository.ListEgressNodes(ctx, "", repository.SortQuery{})
		if err != nil {
			return ProbeBatchResult{}, err
		}
		ids = make([]uint64, 0, len(nodes))
		for _, node := range nodes {
			if node.Enabled && node.EncryptedProxyURL != "" {
				ids = append(ids, node.ID)
			}
		}
	}
	ids = uniqueIDs(ids)
	if len(ids) > maxManualProbeNodes {
		return ProbeBatchResult{}, fmt.Errorf("%w: Maksimum menguji %d proksi setiap kali", ErrInvalidInput, maxManualProbeNodes)
	}
	result := ProbeBatchResult{Requested: len(ids)}
	if len(ids) == 0 {
		return result, nil
	}
	var mu sync.Mutex
	jobs := make(chan uint64)
	var workers sync.WaitGroup
	for range min(maxConcurrentProbes, len(ids)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for id := range jobs {
				probe, err := s.TestNode(ctx, id)
				mu.Lock()
				if err == nil && probe.Status == domain.ProbeStatusHealthy {
					result.Healthy++
				} else {
					result.Unhealthy++
				}
				mu.Unlock()
			}
		}()
	}
	for _, id := range ids {
		select {
		case jobs <- id:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return result, ctx.Err()
		}
	}
	close(jobs)
	workers.Wait()
	return result, nil
}

func (s *Service) OperationsConfig(ctx context.Context) (domain.OperationsConfig, error) {
	operations, err := s.operationsRepository()
	if err != nil {
		return domain.OperationsConfig{}, err
	}
	return operations.GetEgressOperationsConfig(ctx)
}

func (s *Service) UpdateOperationsConfig(ctx context.Context, input OperationsConfigInput) (domain.OperationsConfig, error) {
	operations, err := s.operationsRepository()
	if err != nil {
		return domain.OperationsConfig{}, err
	}
	if input.ProbeIntervalSeconds < 60 || input.ProbeIntervalSeconds > 86400 || input.AssignmentIntervalSeconds < 60 || input.AssignmentIntervalSeconds > 86400 {
		return domain.OperationsConfig{}, fmt.Errorf("%w: Selang tugasan automatik mesti antara 60 hingga 86400 saat", ErrInvalidInput)
	}
	current, err := operations.GetEgressOperationsConfig(ctx)
	if err != nil {
		return domain.OperationsConfig{}, err
	}
	probeProvider := input.ProbeProvider
	if probeProvider == "" {
		probeProvider = current.ProbeProvider.Normalized()
	}
	if !probeProvider.IsValid() {
		return domain.OperationsConfig{}, fmt.Errorf("%w: Perkhidmatan pengesanan proksi tidak disokong", ErrInvalidInput)
	}
	fallbacks := current.Fallbacks
	if input.Fallbacks != nil {
		fallbacks, err = s.validateFallbacks(ctx, current, input.Fallbacks)
		if err != nil {
			return domain.OperationsConfig{}, err
		}
	}
	saved, err := operations.SaveEgressOperationsConfig(ctx, domain.OperationsConfig{
		ProbeProvider: probeProvider, ProbeIntervalSeconds: input.ProbeIntervalSeconds, AutoAssignEnabled: input.AutoAssignEnabled,
		AutoBalanceEnabled: input.AutoBalanceEnabled, AssignmentIntervalSeconds: input.AssignmentIntervalSeconds,
		Fallbacks: fallbacks, UpdatedAt: time.Now().UTC(),
	})
	if errors.Is(err, repository.ErrEgressFallbackInUse) {
		return domain.OperationsConfig{}, fmt.Errorf("%w: Node fallback tetap mesti kekal diaktifkan dan tersedia", ErrInvalidInput)
	}
	if err == nil {
		s.invalidateOperationsConfig()
	}
	return saved, err
}

func (s *Service) validateFallbacks(ctx context.Context, current domain.OperationsConfig, input map[domain.Scope]FallbackConfigInput) (map[domain.Scope]domain.FallbackConfig, error) {
	result := make(map[domain.Scope]domain.FallbackConfig, len(allOperationScopes()))
	for _, scope := range allOperationScopes() {
		result[scope] = current.FallbackFor(scope)
	}
	for scope, fallback := range input {
		if !validScope(scope) {
			return nil, fmt.Errorf("%w: Skop fallback tidak sah", ErrInvalidInput)
		}
		mode := fallback.Mode.Normalized()
		if !mode.IsValid() {
			return nil, fmt.Errorf("%w: Mod fallback tidak sah", ErrInvalidInput)
		}
		switch mode {
		case domain.FallbackModeNone, domain.FallbackModeDirect:
			if fallback.NodeID != 0 {
				return nil, fmt.Errorf("%w: Hanya fallback proksi tetap boleh menentukan node", ErrInvalidInput)
			}
		case domain.FallbackModeFixed:
			if fallback.NodeID == 0 {
				return nil, fmt.Errorf("%w: Fallback proksi tetap mesti menentukan node", ErrInvalidInput)
			}
			node, err := s.repository.GetEgressNode(ctx, fallback.NodeID)
			if errors.Is(err, repository.ErrNotFound) {
				return nil, fmt.Errorf("%w: Node fallback tetap tidak wujud", ErrInvalidInput)
			}
			if err != nil {
				return nil, err
			}
			if err := s.validateFixedFallbackNode(scope, node, true); err != nil {
				return nil, err
			}
		}
		result[scope] = domain.FallbackConfig{Mode: mode, NodeID: fallback.NodeID}
	}
	return result, nil
}

func (s *Service) validateFixedFallbackNode(scope domain.Scope, node domain.Node, rejectCooldown bool) error {
	if !domain.SupportsScope(node.Scope, scope) {
		return fmt.Errorf("%w: Node fallback tetap tidak serasi dengan skop %s", ErrInvalidInput, scope)
	}
	if !node.Enabled || strings.TrimSpace(node.EncryptedProxyURL) == "" {
		return fmt.Errorf("%w: Node fallback tetap mesti diaktifkan dan dikonfigurasi dengan alamat proksi", ErrInvalidInput)
	}
	if node.ProxyPool {
		return fmt.Errorf("%w: Node fallback tetap tidak boleh menggunakan mod kolam proksi", ErrInvalidInput)
	}
	if rejectCooldown && node.CooldownUntil != nil && time.Now().UTC().Before(*node.CooldownUntil) {
		return fmt.Errorf("%w: Node fallback tetap sedang menyejuk", ErrInvalidInput)
	}
	proxyURL, err := s.cipher.Decrypt(node.EncryptedProxyURL)
	if err != nil {
		return fmt.Errorf("%w: Konfigurasi proksi node fallback tetap tidak sah", ErrInvalidInput)
	}
	proxyURL, err = NormalizeProxyURL(proxyURL)
	if err != nil || proxyURL == "" {
		return fmt.Errorf("%w: Alamat proksi node fallback tetap tidak sah", ErrInvalidInput)
	}
	if strings.Contains(proxyURL, ProxyAccountPlaceholder) {
		return fmt.Errorf("%w: Node fallback tetap tidak boleh menggunakan templat proksi akaun", ErrInvalidInput)
	}
	return nil
}

func (s *Service) applySourceInput(value domain.SubscriptionSource, input SubscriptionSourceInput, create bool) (domain.SubscriptionSource, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 160 {
		return domain.SubscriptionSource{}, fmt.Errorf("%w: Nama langganan mesti antara 1 hingga 160 aksara", ErrInvalidInput)
	}
	if !validScope(input.Scope) {
		return domain.SubscriptionSource{}, fmt.Errorf("%w: Skop langganan tidak sah", ErrInvalidInput)
	}
	value.Name, value.Scope, value.Enabled = name, input.Scope, input.Enabled
	if input.RefreshIntervalSeconds != nil {
		if *input.RefreshIntervalSeconds < 60 || *input.RefreshIntervalSeconds > 86400 {
			return domain.SubscriptionSource{}, fmt.Errorf("%w: Selang penyegaran langganan mesti antara 60 hingga 86400 saat", ErrInvalidInput)
		}
		value.RefreshIntervalSeconds = *input.RefreshIntervalSeconds
	}
	if value.RefreshIntervalSeconds == 0 {
		value.RefreshIntervalSeconds = defaultProbeIntervalSeconds
	}
	if input.DefaultAccountCapacity != nil {
		if *input.DefaultAccountCapacity < 0 || *input.DefaultAccountCapacity > maxEgressAccountCapacity {
			return domain.SubscriptionSource{}, fmt.Errorf("%w: Kapasiti akaun setiap proksi mesti antara 0 hingga %d", ErrInvalidInput, maxEgressAccountCapacity)
		}
		value.DefaultAccountCapacity = *input.DefaultAccountCapacity
	}
	if input.ClearURL {
		value.EncryptedURL = ""
	} else if input.URL != nil {
		urlValue, err := normalizeSubscriptionURL(*input.URL)
		if err != nil {
			return domain.SubscriptionSource{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		encryptedURL, err := s.cipher.Encrypt(urlValue)
		if err != nil {
			return domain.SubscriptionSource{}, err
		}
		value.EncryptedURL = encryptedURL
	}
	if create && value.EncryptedURL == "" {
		return domain.SubscriptionSource{}, fmt.Errorf("%w: Alamat langganan mesti disertakan", ErrInvalidInput)
	}
	if input.ClearProxyURL {
		value.EncryptedProxyURL = ""
	} else if input.ProxyURL != nil {
		proxyURL, err := NormalizeProxyURL(*input.ProxyURL)
		if err != nil {
			return domain.SubscriptionSource{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		if proxyURL == "" {
			return domain.SubscriptionSource{}, fmt.Errorf("%w: Alamat proksi langganan tak boleh kosong", ErrInvalidInput)
		}
		if strings.Contains(proxyURL, ProxyAccountPlaceholder) {
			return domain.SubscriptionSource{}, fmt.Errorf("%w: Alamat proksi langganan tidak boleh mengandungi pemegang tempat akaun", ErrInvalidInput)
		}
		encryptedProxyURL, err := s.cipher.Encrypt(proxyURL)
		if err != nil {
			return domain.SubscriptionSource{}, err
		}
		value.EncryptedProxyURL = encryptedProxyURL
	}
	if create || input.URL != nil || input.ClearURL || input.ProxyURL != nil || input.ClearProxyURL {
		value.NextSyncAt = nil
		value.LastSyncError = ""
	}
	return value, nil
}

func publicSource(value domain.SubscriptionSource) domain.PublicSubscriptionSource {
	return domain.PublicSubscriptionSource{
		ID: value.ID, Name: value.Name, Scope: value.Scope, Enabled: value.Enabled, URLConfigured: value.EncryptedURL != "",
		ProxyConfigured:        value.EncryptedProxyURL != "",
		RefreshIntervalSeconds: value.RefreshIntervalSeconds, DefaultAccountCapacity: value.DefaultAccountCapacity,
		LastSyncedAt: value.LastSyncedAt, NextSyncAt: value.NextSyncAt, LastSyncImported: value.LastSyncImported, LastSyncError: value.LastSyncError,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func validScope(scope domain.Scope) bool {
	return scope == domain.ScopeBuild || scope == domain.ScopeWeb || scope == domain.ScopeConsole || scope == domain.ScopeWebAsset || scope == domain.ScopeConsoleAsset
}

func allOperationScopes() []domain.Scope {
	return []domain.Scope{domain.ScopeBuild, domain.ScopeWeb, domain.ScopeConsole, domain.ScopeWebAsset, domain.ScopeConsoleAsset}
}

func validateImportInput(input ImportInput) error {
	if strings.TrimSpace(input.Name) == "" || len(strings.TrimSpace(input.Name)) > 150 || !validScope(input.Scope) || input.AccountCapacity < 0 || input.AccountCapacity > maxEgressAccountCapacity || strings.TrimSpace(input.Content) == "" {
		return fmt.Errorf("%w: Parameter import pukal tidak sah", ErrInvalidInput)
	}
	return nil
}
