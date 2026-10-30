package model

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

type Token struct {
	Id                 int     `json:"id"`
	UserId             int     `json:"user_id" gorm:"index"`
	Key                string  `json:"key" gorm:"type:varchar(128);uniqueIndex"`
	Status             int     `json:"status" gorm:"default:1"`
	Name               string  `json:"name" gorm:"index" `
	CreatedTime        int64   `json:"created_time" gorm:"bigint"`
	AccessedTime       int64   `json:"accessed_time" gorm:"bigint"`
	ExpiredTime        int64   `json:"expired_time" gorm:"bigint;default:-1"` // -1 means never expired
	RemainQuota        int     `json:"remain_quota" gorm:"default:0"`
	UnlimitedQuota     bool    `json:"unlimited_quota"`
	ModelLimitsEnabled bool    `json:"model_limits_enabled"`
	ModelLimits        string  `json:"model_limits" gorm:"type:text"`
	AllowIps           *string `json:"allow_ips" gorm:"default:''"`
	UsedQuota          int     `json:"used_quota" gorm:"default:0"` // used quota
	Group              string  `json:"group" gorm:"default:''"`
	CrossGroupRetry    bool    `json:"cross_group_retry"` // 跨分组重试，仅auto分组有效
	AutoGroups         string  `json:"-" gorm:"type:text"`
	// 允许用此令牌查询所属用户的额度；新建令牌默认开启，已有令牌升级后保持关闭
	AllowUserQuotaQuery     bool           `json:"allow_user_quota_query"`
	QuotaResetPeriod        string         `json:"quota_reset_period" gorm:"type:varchar(16);default:'never'"`
	QuotaResetCustomSeconds int64          `json:"quota_reset_custom_seconds" gorm:"type:bigint;default:0"`
	LastResetTime           int64          `json:"last_reset_time" gorm:"type:bigint;default:0"`
	NextResetTime           int64          `json:"next_reset_time" gorm:"type:bigint;default:0;index"`
	DeletedAt               gorm.DeletedAt `gorm:"index"`
}

// RecalculateTokenQuotaReset applies the normalized reset configuration and
// starts a fresh schedule from now. Request-provided timestamps are ignored.
func RecalculateTokenQuotaReset(token *Token, now int64) {
	if token == nil {
		return
	}
	token.QuotaResetPeriod = NormalizeResetPeriod(token.QuotaResetPeriod)
	if token.UnlimitedQuota {
		token.QuotaResetPeriod = SubscriptionResetNever
	}
	if token.QuotaResetPeriod != SubscriptionResetCustom {
		token.QuotaResetCustomSeconds = 0
	}
	token.LastResetTime = 0
	token.NextResetTime = 0
	if token.QuotaResetPeriod == SubscriptionResetNever {
		return
	}
	nextReset := calcQuotaResetNextTime(time.Unix(now, 0), token.QuotaResetPeriod, token.QuotaResetCustomSeconds, 0)
	if nextReset > 0 {
		token.LastResetTime = now
		token.NextResetTime = nextReset
	}
}

func (token *Token) GetAutoGroups() ([]string, error) {
	if token.AutoGroups == "" {
		return nil, nil
	}
	var groups []string
	if err := common.UnmarshalJsonStr(token.AutoGroups, &groups); err != nil {
		return nil, err
	}
	return groups, nil
}

func (token *Token) SetAutoGroups(groups []string) error {
	if len(groups) == 0 {
		token.AutoGroups = ""
		return nil
	}
	data, err := common.Marshal(groups)
	if err != nil {
		return err
	}
	token.AutoGroups = string(data)
	return nil
}

func (token *Token) Clean() {
	token.Key = ""
}

func MaskTokenKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return strings.Repeat("*", len(key))
	}
	if len(key) <= 8 {
		return key[:2] + "****" + key[len(key)-2:]
	}
	return key[:4] + "**********" + key[len(key)-4:]
}

func (token *Token) GetFullKey() string {
	return token.Key
}

func (token *Token) GetMaskedKey() string {
	return MaskTokenKey(token.Key)
}

func (token *Token) GetIpLimits() []string {
	// delete empty spaces
	//split with \n
	ipLimits := make([]string, 0)
	if token.AllowIps == nil {
		return ipLimits
	}
	cleanIps := strings.ReplaceAll(*token.AllowIps, " ", "")
	if cleanIps == "" {
		return ipLimits
	}
	ips := strings.SplitSeq(cleanIps, "\n")
	for ip := range ips {
		ip = strings.TrimSpace(ip)
		ip = strings.ReplaceAll(ip, ",", "")
		if ip != "" {
			ipLimits = append(ipLimits, ip)
		}
	}
	return ipLimits
}

func GetAllUserTokens(userId int, startIdx int, num int) ([]*Token, error) {
	var tokens []*Token
	var err error
	err = DB.Where("user_id = ?", userId).Order("id desc").Limit(num).Offset(startIdx).Find(&tokens).Error
	return tokens, err
}

// sanitizeLikePattern 校验并清洗用户输入的 LIKE 搜索模式。
// 规则：
//  1. 转义 ! 和 _（使用 ! 作为 ESCAPE 字符，兼容 MySQL/PostgreSQL/SQLite）
//  2. 连续的 % 合并为单个 %
//  3. 最多允许 2 个 %
//  4. 含 % 时（模糊搜索），去掉 % 后关键词长度必须 >= 2
//  5. 不含 % 时按精确匹配
func sanitizeLikePattern(input string) (string, error) {
	// 1. 先转义 ESCAPE 字符 ! 自身，再转义 _
	//    使用 ! 而非 \ 作为 ESCAPE 字符，避免 MySQL 中反斜杠的字符串转义问题
	input = strings.ReplaceAll(input, "!", "!!")
	input = strings.ReplaceAll(input, `_`, `!_`)

	if err := validateLikePattern(input); err != nil {
		return "", err
	}

	// 5. 无 % 时，精确全匹配
	return input, nil
}

func validateLikePattern(input string) error {
	// 1. 连续的 % 直接拒绝
	if strings.Contains(input, "%%") {
		return common.NewMessage("The search pattern cannot contain consecutive % wildcards")
	}

	// 2. 统计 % 数量，不得超过 2
	count := strings.Count(input, "%")
	if count > 2 {
		return common.NewMessage("The search pattern can contain at most 2 % wildcards")
	}

	// 3. 含 % 时，去掉 % 后关键词长度必须 >= 2
	if count > 0 {
		stripped := strings.ReplaceAll(input, "%", "")
		if len(stripped) < 2 {
			return common.NewMessage("Fuzzy search keywords must be at least 2 characters long")
		}
	}

	return nil
}

const searchHardLimit = 100

func SearchUserTokens(userId int, keyword string, token string, offset int, limit int) (tokens []*Token, total int64, err error) {
	// model 层强制截断
	if limit <= 0 || limit > searchHardLimit {
		limit = searchHardLimit
	}
	if offset < 0 {
		offset = 0
	}

	if token != "" {
		token = strings.TrimPrefix(token, "sk-")
	}

	// 超量用户（令牌数超过上限）只允许精确搜索，禁止模糊搜索
	maxTokens := operation_setting.GetMaxUserTokens()
	hasFuzzy := strings.Contains(keyword, "%") || strings.Contains(token, "%")
	if hasFuzzy {
		count, err := CountUserTokens(userId)
		if err != nil {
			common.SysLog(common.LogText("failed to count user tokens: %s", err.Error()))
			return nil, 0, common.NewMessage("Failed to get the token count")
		}
		if int(count) > maxTokens {
			return nil, 0, common.NewMessage("Token count exceeds the limit. Only exact search is allowed, do not use the % wildcard")
		}
	}

	baseQuery := DB.Model(&Token{}).Where("user_id = ?", userId)

	// 非空才加 LIKE 条件，空则跳过（不过滤该字段）
	if keyword != "" {
		keywordPattern, err := sanitizeLikePattern(keyword)
		if err != nil {
			return nil, 0, err
		}
		baseQuery = baseQuery.Where("name LIKE ? ESCAPE '!'", keywordPattern)
	}
	if token != "" {
		tokenPattern, err := sanitizeLikePattern(token)
		if err != nil {
			return nil, 0, err
		}
		baseQuery = baseQuery.Where(commonKeyCol+" LIKE ? ESCAPE '!'", tokenPattern)
	}

	// 先查匹配总数（用于分页，受 maxTokens 上限保护，避免全表 COUNT）
	err = baseQuery.Limit(maxTokens).Count(&total).Error
	if err != nil {
		common.SysError(common.LogText("failed to count search tokens: %s", err.Error()))
		return nil, 0, common.NewMessage("Failed to search tokens")
	}

	// 再分页查数据
	err = baseQuery.Order("id desc").Offset(offset).Limit(limit).Find(&tokens).Error
	if err != nil {
		common.SysError(common.LogText("failed to search tokens: %s", err.Error()))
		return nil, 0, common.NewMessage("Failed to search tokens")
	}
	return tokens, total, nil
}

func ValidateUserToken(key string) (token *Token, err error) {
	if key == "" {
		return nil, ErrTokenNotProvided
	}
	token, err = GetTokenByKey(key, false)
	if err == nil {
		if token.Status == common.TokenStatusExhausted ||
			token.Status == common.TokenStatusExpired ||
			token.Status != common.TokenStatusEnabled {
			return token, ErrTokenInvalid
		}
		now := common.GetTimestamp()
		if token.ExpiredTime != -1 && token.ExpiredTime < now {
			if !common.RedisEnabled {
				result := DB.Model(&Token{}).
					Where("id = ? AND expired_time != -1 AND expired_time <= ?", token.Id, now).
					Update("status", common.TokenStatusExpired)
				if result.Error != nil {
					common.SysLog(common.LogText("failed to update token status: %s", result.Error.Error()))
				}
			}
			return token, ErrTokenInvalid
		}
		if !token.UnlimitedQuota && token.RemainQuota <= 0 {
			if !common.RedisEnabled {
				result := DB.Model(&Token{}).
					Where("id = ? AND unlimited_quota = ? AND remain_quota <= 0", token.Id, false).
					Update("status", common.TokenStatusExhausted)
				if result.Error != nil {
					common.SysLog(common.LogText("failed to update token status: %s", result.Error.Error()))
				}
			}
			return token, ErrTokenInvalid
		}
		return token, nil
	}
	common.SysLog(common.LogText("ValidateUserToken: failed to get token: %s", err.Error()))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTokenInvalid
	}
	return nil, fmt.Errorf("%w: %v", ErrDatabase, err)
}

func GetTokenByIds(id int, userId int) (*Token, error) {
	if id == 0 || userId == 0 {
		return nil, common.NewMessage("Token ID or user ID is empty")
	}
	token := Token{Id: id, UserId: userId}
	var err error = nil
	err = DB.First(&token, "id = ? and user_id = ?", id, userId).Error
	return &token, err
}

func GetTokenById(id int) (*Token, error) {
	if id == 0 {
		return nil, errors.New("token id is empty")
	}
	token := Token{Id: id}
	var err error = nil
	err = DB.First(&token, "id = ?", id).Error
	return &token, err
}

func GetTokenByKey(key string, fromDB bool) (token *Token, err error) {
	if !fromDB && common.RedisEnabled {
		// Try Redis first
		token, err := cacheGetTokenByKey(key)
		if err == nil {
			return token, nil
		}
		// Don't return error - fall through to DB
	}
	token = &Token{}
	if err = DB.Where(commonKeyCol+" = ?", key).First(token).Error; err != nil {
		return nil, err
	}
	if common.RedisEnabled {
		// 冷缓存时用数据库快照初始化；已存在的哈希只刷新 TTL，
		// 避免快照覆盖 Redis 中已被原子预扣的余额。初始化失败不影响本次读取。
		if _, cacheErr := cacheInitToken(*token); cacheErr != nil {
			common.SysLog(common.LogText("failed to init token cache: %s", cacheErr.Error()))
		}
	}
	return token, nil
}

func advanceTokenQuotaResetSchedule(token *Token, now int64) (bool, int64, int64) {
	period := NormalizeResetPeriod(token.QuotaResetPeriod)
	if period == SubscriptionResetNever {
		return false, 0, 0
	}

	baseUnix := token.LastResetTime
	advanced := false
	if baseUnix <= 0 {
		if token.NextResetTime > 0 && token.NextResetTime <= now {
			baseUnix = token.NextResetTime
			advanced = true
		} else {
			baseUnix = now
		}
	}
	base := time.Unix(baseUnix, 0)
	nextReset := calcQuotaResetNextTime(base, period, token.QuotaResetCustomSeconds, 0)
	if nextReset == 0 || nextReset > now {
		return advanced, baseUnix, nextReset
	}

	if period == SubscriptionResetCustom {
		customSeconds := token.QuotaResetCustomSeconds
		if customSeconds <= 0 {
			return false, baseUnix, 0
		}
		if baseUnix < 0 && now > math.MaxInt64+baseUnix {
			return false, baseUnix, 0
		}
		elapsed := now - baseUnix
		steps := elapsed / customSeconds
		lastReset := baseUnix + steps*customSeconds
		if lastReset > math.MaxInt64-customSeconds {
			return true, lastReset, 0
		}
		return true, lastReset, lastReset + customSeconds
	}

	var periodStart time.Time
	nowTime := time.Unix(now, 0)
	switch period {
	case SubscriptionResetDaily:
		periodStart = time.Date(nowTime.Year(), nowTime.Month(), nowTime.Day(), 0, 0, 0, 0, nowTime.Location())
	case SubscriptionResetWeekly:
		weekday := int(nowTime.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		periodStart = time.Date(nowTime.Year(), nowTime.Month(), nowTime.Day(), 0, 0, 0, 0, nowTime.Location()).
			AddDate(0, 0, 1-weekday)
	case SubscriptionResetMonthly:
		periodStart = time.Date(nowTime.Year(), nowTime.Month(), 1, 0, 0, 0, 0, nowTime.Location())
	default:
		return false, baseUnix, 0
	}
	return true, periodStart.Unix(), calcQuotaResetNextTime(periodStart, period, 0, 0)
}

func resetTokenQuotaTx(tx *gorm.DB, token *Token, now int64) (bool, error) {
	if tx == nil || token == nil || token.NextResetTime <= 0 || token.NextResetTime > now {
		return false, nil
	}
	if token.UnlimitedQuota || NormalizeResetPeriod(token.QuotaResetPeriod) == SubscriptionResetNever {
		return false, tx.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]any{
			"last_reset_time": 0,
			"next_reset_time": 0,
		}).Error
	}

	advanced, lastResetTime, nextResetTime := advanceTokenQuotaResetSchedule(token, now)
	if !advanced {
		if token.LastResetTime == lastResetTime && token.NextResetTime == nextResetTime {
			return false, nil
		}
		return false, tx.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]any{
			"last_reset_time": lastResetTime,
			"next_reset_time": nextResetTime,
		}).Error
	}

	remainQuota := token.RemainQuota
	if token.UsedQuota > 0 {
		maxInt := int(^uint(0) >> 1)
		if remainQuota > 0 && token.UsedQuota > maxInt-remainQuota {
			return false, fmt.Errorf("token quota total overflows for token %d", token.Id)
		}
		remainQuota += token.UsedQuota
	}
	status := token.Status
	if status == common.TokenStatusExhausted && remainQuota > 0 &&
		(token.ExpiredTime == -1 || token.ExpiredTime > now) {
		status = common.TokenStatusEnabled
	}
	return true, tx.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]any{
		"remain_quota":    remainQuota,
		"used_quota":      0,
		"status":          status,
		"last_reset_time": lastResetTime,
		"next_reset_time": nextResetTime,
	}).Error
}

// ResetDueTokens resets token quota cycles whose next_reset_time has passed.
func ResetDueTokens(limit int) (int, error) {
	if limit <= 0 {
		limit = 200
	}
	now := GetDBTimestamp()
	resetBefore := now
	// Other nodes may still hold pre-reset quota deltas in their process-local
	// batch stores. Give those stores at least two flush intervals to land
	// before preserving the old cycle's allocation.
	if common.BatchUpdateEnabled {
		graceSeconds := int64(max(common.BatchUpdateInterval*2, 10))
		graceSeconds = min(graceSeconds, int64(60))
		resetBefore -= graceSeconds
	}
	var tokens []Token
	if err := DB.Where("next_reset_time > 0 AND next_reset_time <= ?", resetBefore).
		Order("next_reset_time asc").
		Limit(limit).
		Find(&tokens).Error; err != nil {
		return 0, err
	}
	if len(tokens) == 0 {
		return 0, nil
	}

	eligible := make([]Token, 0, len(tokens))
	for i := range tokens {
		if err := invalidateTokenCacheForMutation(tokens[i].Key); err != nil {
			common.SysLog(fmt.Sprintf("failed to invalidate token cache before quota reset for token %d: %v", tokens[i].Id, err))
		}
		eligible = append(eligible, tokens[i])
	}
	// Flush pending quota deltas before taking row locks so usage from the
	// previous cycle is included in the allocation being preserved.
	if common.BatchUpdateEnabled {
		batchUpdate()
	}

	resetCount := 0
	for _, token := range eligible {
		var reset bool
		err := DB.Transaction(func(tx *gorm.DB) error {
			var locked Token
			if err := lockForUpdate(tx).
				Where("id = ? AND next_reset_time > 0 AND next_reset_time <= ?", token.Id, resetBefore).
				First(&locked).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}
			var err error
			reset, err = resetTokenQuotaTx(tx, &locked, now)
			return err
		})
		if err != nil {
			return resetCount, err
		}
		if !reset {
			continue
		}
		resetCount++
		if err := invalidateTokenCacheForMutation(token.Key); err != nil {
			common.SysLog(fmt.Sprintf("failed to invalidate token cache after quota reset for token %d: %v", token.Id, err))
		}
	}
	return resetCount, nil
}

func (token *Token) Insert() error {
	var err error
	err = DB.Create(token).Error
	return err
}

// Update Make sure your token's fields is completed, because this will update non-zero values
func (token *Token) Update() (err error) {
	// 写库前失效缓存并设置 fence，防止并发读者把过期快照重新写回缓存。
	if cacheErr := invalidateTokenCacheForMutation(token.Key); cacheErr != nil {
		common.SysLog(common.LogText("failed to invalidate token cache before update: %s", cacheErr.Error()))
	}
	return DB.Model(token).Select("name", "status", "expired_time", "remain_quota", "unlimited_quota",
		"model_limits_enabled", "model_limits", "allow_ips", "group", "cross_group_retry", "auto_groups",
		"allow_user_quota_query").Updates(token).Error
}

var tokenMutableUpdateColumns = []string{
	"name", "status", "expired_time", "remain_quota", "unlimited_quota",
	"model_limits_enabled", "model_limits", "allow_ips", "group", "cross_group_retry", "auto_groups",
	"allow_user_quota_query", "quota_reset_period", "quota_reset_custom_seconds", "last_reset_time", "next_reset_time",
}

// UpdateUserTokenWithLock applies a user-scoped token mutation while holding a
// row lock, so a concurrent scheduled quota reset cannot be overwritten by a
// stale controller read.
func UpdateUserTokenWithLock(userId int, tokenId int, mutate func(*Token) error) (*Token, error) {
	if userId <= 0 || tokenId <= 0 {
		return nil, errors.New("invalid token update args")
	}
	if mutate == nil {
		return nil, errors.New("token update mutation is required")
	}
	var tokenKey string
	keyResult := DB.Model(&Token{}).
		Where("id = ? AND user_id = ?", tokenId, userId).
		Limit(1).
		Pluck(commonKeyCol, &tokenKey)
	if keyResult.Error != nil {
		return nil, keyResult.Error
	}
	if keyResult.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	if cacheErr := invalidateTokenCacheForMutation(tokenKey); cacheErr != nil {
		common.SysLog(common.LogText("failed to invalidate token cache before locked update: %s", cacheErr.Error()))
	}

	var token Token
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", tokenId, userId).First(&token).Error; err != nil {
			return err
		}
		if err := mutate(&token); err != nil {
			return err
		}
		return tx.Model(&token).Select(tokenMutableUpdateColumns).Updates(&token).Error
	})
	if err != nil {
		return nil, err
	}
	if cacheErr := invalidateTokenCacheForMutation(token.Key); cacheErr != nil {
		common.SysLog(common.LogText("failed to invalidate token cache after locked update: %s", cacheErr.Error()))
	}
	return &token, nil
}

func (token *Token) SelectUpdate() (err error) {
	if cacheErr := invalidateTokenCacheForMutation(token.Key); cacheErr != nil {
		common.SysLog(common.LogText("failed to invalidate token cache before status update: %s", cacheErr.Error()))
	}
	// This can update zero values
	return DB.Model(token).Select("accessed_time", "status").Updates(token).Error
}

func (token *Token) Delete() (err error) {
	if cacheErr := invalidateTokenCacheForMutation(token.Key); cacheErr != nil {
		common.SysLog(common.LogText("failed to invalidate token cache before delete: %s", cacheErr.Error()))
	}
	return DB.Delete(token).Error
}

func (token *Token) IsModelLimitsEnabled() bool {
	return token.ModelLimitsEnabled
}

func (token *Token) GetModelLimits() []string {
	if token.ModelLimits == "" {
		return []string{}
	}
	return strings.Split(token.ModelLimits, ",")
}

func (token *Token) GetModelLimitsMap() map[string]bool {
	limits := token.GetModelLimits()
	limitsMap := make(map[string]bool)
	for _, limit := range limits {
		limitsMap[limit] = true
	}
	return limitsMap
}

func DisableModelLimits(tokenId int) error {
	token, err := GetTokenById(tokenId)
	if err != nil {
		return err
	}
	token.ModelLimitsEnabled = false
	token.ModelLimits = ""
	return token.Update()
}

func DeleteTokenById(id int, userId int) (err error) {
	// Why we need userId here? In case user want to delete other's token.
	if id == 0 || userId == 0 {
		return common.NewMessage("Token ID or user ID is empty")
	}
	token := Token{Id: id, UserId: userId}
	err = DB.Where(token).First(&token).Error
	if err != nil {
		return err
	}
	return token.Delete()
}

func IncreaseTokenQuota(tokenId int, key string, quota int) (err error) {
	if quota < 0 {
		return errors.New("quota cannot be negative")
	}
	if common.RedisEnabled {
		gopool.Go(func() {
			// 守卫式增量：哈希不存在时跳过，由下次读取从数据库水合，
			// 绝不创建只有配额字段的残缺哈希。
			if _, err := cacheApplyTokenQuotaDelta(tokenId, key, int64(quota)); err != nil {
				common.SysLog(common.LogText("failed to increase token quota: %s", err.Error()))
			}
		})
	}
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeTokenQuota, tokenId, quota)
		return nil
	}
	return increaseTokenQuota(tokenId, quota)
}

func increaseTokenQuota(id int, quota int) (err error) {
	err = DB.Model(&Token{}).Where("id = ?", id).Updates(
		map[string]any{
			"remain_quota":  gorm.Expr("remain_quota + ?", quota),
			"used_quota":    gorm.Expr("used_quota - ?", quota),
			"accessed_time": common.GetTimestamp(),
		},
	).Error
	return err
}

func DecreaseTokenQuota(id int, key string, quota int) (err error) {
	if quota < 0 {
		return errors.New("quota cannot be negative")
	}
	if common.RedisEnabled {
		gopool.Go(func() {
			if _, err := cacheApplyTokenQuotaDelta(id, key, int64(-quota)); err != nil {
				common.SysLog(common.LogText("failed to decrease token quota: %s", err.Error()))
			}
		})
	}
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeTokenQuota, id, -quota)
		return nil
	}
	return decreaseTokenQuota(id, quota)
}

func decreaseTokenQuota(id int, quota int) (err error) {
	err = DB.Model(&Token{}).Where("id = ?", id).Updates(
		map[string]any{
			"remain_quota":  gorm.Expr("remain_quota - ?", quota),
			"used_quota":    gorm.Expr("used_quota + ?", quota),
			"accessed_time": common.GetTimestamp(),
		},
	).Error
	return err
}

// CountUserTokens returns total number of tokens for the given user, used for pagination
func CountUserTokens(userId int) (int64, error) {
	var total int64
	err := DB.Model(&Token{}).Where("user_id = ?", userId).Count(&total).Error
	return total, err
}

// BatchDeleteTokens 删除指定用户的一组令牌，返回成功删除数量
func BatchDeleteTokens(ids []int, userId int) (int, error) {
	if len(ids) == 0 {
		return 0, common.NewMessage("Token IDs cannot be empty")
	}

	tx := DB.Begin()

	var tokens []Token
	if err := tx.Where("user_id = ? AND id IN (?)", userId, ids).Find(&tokens).Error; err != nil {
		tx.Rollback()
		return 0, err
	}
	if err := invalidateTokensCache(tokens); err != nil {
		common.SysLog(common.LogText("failed to invalidate token cache before batch delete: %s", err.Error()))
	}

	if err := tx.Where("user_id = ? AND id IN (?)", userId, ids).Delete(&Token{}).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}

	return len(tokens), nil
}

func GetTokenKeysByIds(ids []int, userId int) ([]Token, error) {
	var tokens []Token
	err := DB.Select("id", commonKeyCol).
		Where("user_id = ? AND id IN (?)", userId, ids).
		Find(&tokens).Error
	return tokens, err
}

// InvalidateUserTokensCache 清理指定用户所有令牌在 Redis 中的缓存，
// 配合 InvalidateUserCache 使用，可在用户被禁用/删除时立即阻断其令牌的请求。
// 下一次请求将从数据库重新加载令牌及用户状态，从而立即识别出被禁用的用户。
func InvalidateUserTokensCache(userId int) error {
	if !common.RedisEnabled {
		return nil
	}
	if userId <= 0 {
		return errors.New("invalid user id")
	}
	var tokens []Token
	if err := DB.Unscoped().
		Select("id", commonKeyCol).
		Where("user_id = ?", userId).
		Find(&tokens).Error; err != nil {
		return err
	}
	return invalidateTokensCache(tokens)
}

func invalidateTokensCache(tokens []Token) error {
	if !common.RedisEnabled {
		return nil
	}
	var firstErr error
	for _, t := range tokens {
		if t.Key == "" {
			continue
		}
		if err := invalidateTokenCacheForMutation(t.Key); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
