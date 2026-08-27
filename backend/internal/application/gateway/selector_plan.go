package gateway

import (
	"container/heap"
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

type candidateScore struct {
	index             int
	webCatalogSupport bool
	tier              int
	preferFreeBuild   bool
	quotaKnown        bool
	quotaAvailable    bool
	billingFresh      bool
	inFlight          int
	remaining         float64
	lastSelected      time.Time
	// thinkingScore is the soft per-account reasoning-quality score
	// (patch #17). Accounts that recently produced thin thinking get a lower
	// score and are tried less often; accounts that produce real reasoning
	// rise. Zero means no observation yet — treated as the neutral default.
	thinkingScore int
	// benakAvoid is set for accounts that currently carry a durable
	// missing-thinking marker. They are kept in the pool but their score is
	// heavily discounted so they are tried last among healthy candidates.
	benakAvoid bool
}

// candidatePlan 使用线性建堆保留完整路由优先级，并允许 claim 失败后按顺序取下一账号。
type candidatePlan struct {
	values []account.RoutingCandidate
	scores []candidateScore
}

func (p *candidatePlan) Len() int { return len(p.scores) }

func (p *candidatePlan) Less(left, right int) bool {
	return candidateScoreBetter(p.values, p.scores[left], p.scores[right])
}

func (p *candidatePlan) Swap(left, right int) {
	p.scores[left], p.scores[right] = p.scores[right], p.scores[left]
}

func (p *candidatePlan) Push(value any) {
	p.scores = append(p.scores, value.(candidateScore))
}

func (p *candidatePlan) Pop() any {
	last := len(p.scores) - 1
	value := p.scores[last]
	p.scores = p.scores[:last]
	return value
}

func (p *candidatePlan) Next() (account.RoutingCandidate, bool) {
	if p == nil || p.Len() == 0 {
		return account.RoutingCandidate{}, false
	}
	score := heap.Pop(p).(candidateScore)
	return p.values[score.index], true
}

func candidateScoreBetter(values []account.RoutingCandidate, leftScore, rightScore candidateScore) bool {
	leftCandidate, rightCandidate := values[leftScore.index], values[rightScore.index]
	left, right := leftCandidate.Credential, rightCandidate.Credential
	leftSupports, rightSupports := leftCandidate.SupportsModel, rightCandidate.SupportsModel
	leftKnown, rightKnown := leftCandidate.ModelCapabilityKnown, rightCandidate.ModelCapabilityKnown
	if leftScore.webCatalogSupport {
		leftSupports, leftKnown = true, true
	}
	if rightScore.webCatalogSupport {
		rightSupports, rightKnown = true, true
	}
	if leftSupports != rightSupports {
		return leftSupports
	}
	if leftKnown != rightKnown {
		return leftKnown
	}
	// preferFreeBuild ialah strategi pengguna — ia dinilai sebelum quota
	// supaya akaun Free yang disahkan tidak dikalahkan oleh akaun paid hanya
	// kerana akaun paid itu ada data quota. Akaun Free tanpa QuotaWindow
	// (tiada data) bukan "tiada quota"; dia hanya "tiada data".
	if leftScore.preferFreeBuild != rightScore.preferFreeBuild {
		return leftScore.preferFreeBuild
	}
	// A synced remote window with remaining quota is a stronger routing signal
	// than priority or tier. Unknown windows remain eligible as a fallback, but
	// cannot displace an account whose requested mode is known to be available.
	if leftScore.quotaAvailable != rightScore.quotaAvailable {
		return leftScore.quotaAvailable
	}
	if leftScore.quotaKnown != rightScore.quotaKnown {
		return leftScore.quotaKnown
	}
	if leftScore.tier != rightScore.tier {
		return leftScore.tier < rightScore.tier
	}
	// Patch #17: within the same tier, prefer accounts that recently produced
	// real thinking over accounts that keep returning thin thinking-only
	// answers. This is a soft ordering, not an exclusion — the pool stays
	// intact, but the highest-quality accounts are tried first.
	if leftScore.thinkingScore != rightScore.thinkingScore {
		return leftScore.thinkingScore > rightScore.thinkingScore
	}
	// Patch #19: accounts with a durable missing-thinking marker are tried
	// last among healthy candidates — a soft quarantine without a cooldown.
	if leftScore.benakAvoid != rightScore.benakAvoid {
		return rightScore.benakAvoid // left healthy wins when right is benakAvoid
	}
	if left.Priority != right.Priority {
		return left.Priority > right.Priority
	}
	if leftScore.billingFresh != rightScore.billingFresh {
		return leftScore.billingFresh
	}
	if leftScore.inFlight != rightScore.inFlight {
		return leftScore.inFlight < rightScore.inFlight
	}
	if leftScore.remaining != rightScore.remaining {
		return leftScore.remaining > rightScore.remaining
	}
	if !leftScore.lastSelected.Equal(rightScore.lastSelected) {
		return leftScore.lastSelected.Before(rightScore.lastSelected)
	}
	return left.ID < right.ID
}

// planCandidates 批量读取动态并发状态，并以 O(n) 建堆生成保持原比较规则的候选计划。
func (s *Selector) planCandidates(ctx context.Context, values []account.RoutingCandidate, now time.Time, tierOrder []account.WebTier) (*candidatePlan, error) {
	return s.planCandidateIndexes(ctx, values, nil, now, tierOrder)
}

// planCandidateIndexes 在不可变候选快照上按下标规划，避免过滤阶段复制完整账号结构。
// indexes 为 nil 时表示使用 values 的全部元素。
func (s *Selector) planCandidateIndexes(ctx context.Context, values []account.RoutingCandidate, indexes []int, now time.Time, tierOrder []account.WebTier) (*candidatePlan, error) {
	return s.planCandidateIndexesWithHints(ctx, values, indexes, now, tierOrder, nil, s.preferFreeBuildEnabled())
}

func (s *Selector) planCandidateIndexesWithHints(ctx context.Context, values []account.RoutingCandidate, indexes []int, now time.Time, tierOrder []account.WebTier, concurrencyHints map[int]int, preferFreeBuild bool) (*candidatePlan, error) {
	length := len(indexes)
	if indexes == nil {
		length = len(values)
	}
	inFlight := make([]int, length)
	if concurrencyHints == nil {
		keys := make([]string, length)
		for position := range length {
			index := position
			if indexes != nil {
				index = indexes[position]
			}
			keys[position] = accountConcurrencyKey(values[index].Credential.ID)
		}
		concurrencySnapshot, err := s.loadConcurrencySnapshot(ctx, keys)
		if err != nil {
			return nil, err
		}
		for position := range length {
			inFlight[position] = concurrencySnapshot[keys[position]]
		}
	} else {
		missingIndexes := make([]int, 0, length)
		keys := make([]string, 0, length)
		for position := range length {
			index := position
			if indexes != nil {
				index = indexes[position]
			}
			if _, exists := concurrencyHints[index]; exists {
				continue
			}
			missingIndexes = append(missingIndexes, index)
			keys = append(keys, accountConcurrencyKey(values[index].Credential.ID))
		}
		if len(keys) > 0 {
			concurrencySnapshot, err := s.loadConcurrencySnapshot(ctx, keys)
			if err != nil {
				return nil, err
			}
			for position, index := range missingIndexes {
				concurrencyHints[index] = concurrencySnapshot[keys[position]]
			}
		}
		for position := range length {
			index := position
			if indexes != nil {
				index = indexes[position]
			}
			inFlight[position] = concurrencyHints[index]
		}
	}

	s.selectionMu.RLock()
	scores := make([]candidateScore, 0, length)
	for position := range length {
		index := position
		if indexes != nil {
			index = indexes[position]
		}
		candidate := values[index]
		limit := candidate.Credential.MaxConcurrent
		if limit <= 0 {
			limit = account.DefaultMaxConcurrent
		}
		// 已知满载的账号不进入计划，避免高优先级满载账号逐个 claim 失败后
		// 才轮到仍有容量的低优先级账号。
		if inFlight[position] >= limit {
			continue
		}
		score := candidateScore{
			index: index, tier: tierOrderRank(tierOrder, candidate.Credential.WebTier),
			webCatalogSupport: candidate.Credential.Provider == account.ProviderWeb && len(tierOrder) > 0 && webTierInOrder(tierOrder, candidate.Credential.WebTier),
			preferFreeBuild:   preferFreeBuild && candidate.IsKnownFreeBuild(),
			inFlight:          inFlight[position], lastSelected: s.lastSelectedAt[candidate.Credential.ID],
		}
		// Patch #17: attach the soft thinking score so thin-thinking accounts
		// drop within their tier without leaving the pool.
		score.thinkingScore = s.thinkingScoreOf(candidate.Credential.ID)
		// Patch #19: a durable missing-thinking marker marks the account as
		// benak-avoid — it is tried last among healthy candidates.
		if candidate.Credential.LastError == account.LastErrorMissingThinking || candidate.Credential.LastError == account.LastErrorMissingThinkingDisabled {
			score.benakAvoid = true
		}
		// 只有真实上游快照能够证明账号具备该模式额度。历史默认值和
		// 本地预测值都属于未知能力，只保留为路由兜底。
		if candidate.QuotaWindow != nil && candidate.QuotaWindow.Source == account.QuotaSourceUpstream {
			score.quotaKnown = true
			score.quotaAvailable = candidate.QuotaWindow.Remaining > 0
		}
		if candidate.Billing != nil {
			score.remaining = candidate.Billing.Remaining()
			score.billingFresh = now.Sub(candidate.Billing.SyncedAt) <= 30*time.Minute
		}
		scores = append(scores, score)
	}
	s.selectionMu.RUnlock()
	plan := &candidatePlan{values: values, scores: scores}
	heap.Init(plan)
	return plan, nil
}

// loadConcurrencySnapshot 在极短窗口内合并相同候选池的并发快照读取。
// 快照只参与排序，最终容量仍由原子 Acquire 校验，因此陈旧快照不会突破账号并发上限。
func (s *Selector) loadConcurrencySnapshot(ctx context.Context, keys []string) (map[string]int, error) {
	cacheKey := concurrencySnapshotKey(keys)
	load := func() (map[string]int, error) {
		if batchReader, ok := s.concurrency.(repository.ConcurrencySnapshotReader); ok {
			values, err := batchReader.CurrentMany(ctx, keys)
			if err != nil {
				return nil, fmt.Errorf("Membaca secara pukal sewa serentak akaun: %w", err)
			}
			return values, nil
		}
		values := make(map[string]int, len(keys))
		for _, key := range keys {
			current, err := s.concurrency.Current(ctx, key)
			if err != nil {
				return nil, fmt.Errorf("Membaca sewa serentak akaun: %w", err)
			}
			values[key] = current
		}
		return values, nil
	}
	// 仅测试中的手工 Selector 可能没有初始化缓存，保持最小兼容回退。
	if s.concurrencySnapshots == nil {
		return load()
	}
	return s.concurrencySnapshots.Load(ctx, cacheKey, time.Now(), load)
}

func concurrencySnapshotKey(keys []string) [32]byte {
	hash := sha256.New()
	separator := []byte{0}
	for _, key := range keys {
		_, _ = hash.Write([]byte(key))
		_, _ = hash.Write(separator)
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func accountConcurrencyKey(accountID uint64) string {
	return repository.AccountConcurrencyKey(accountID)
}
