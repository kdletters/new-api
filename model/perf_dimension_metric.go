package model

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PerfDimensionMetric stores hourly (or configured bucket-sized) relay health
// counters for one administrative reporting dimension.
type PerfDimensionMetric struct {
	Id                 int    `json:"id" gorm:"primaryKey"`
	Dimension          string `json:"dimension" gorm:"size:16;uniqueIndex:idx_perf_dimension_entity_bucket,priority:1"`
	EntityId           int    `json:"entity_id" gorm:"uniqueIndex:idx_perf_dimension_entity_bucket,priority:2"`
	EntityName         string `json:"entity_name" gorm:"size:128"`
	BucketTs           int64  `json:"bucket_ts" gorm:"uniqueIndex:idx_perf_dimension_entity_bucket,priority:3;index:idx_perf_dimension_bucket_ts"`
	RequestCount       int64  `json:"request_count" gorm:"default:0"`
	SuccessCount       int64  `json:"success_count" gorm:"default:0"`
	CacheEligibleCount int64  `json:"cache_eligible_count" gorm:"default:0"`
	CacheHitCount      int64  `json:"cache_hit_count" gorm:"default:0"`
	InputTokens        int64  `json:"input_tokens" gorm:"default:0"`
	CachedTokens       int64  `json:"cached_tokens" gorm:"default:0"`
}

func (PerfDimensionMetric) TableName() string {
	return "perf_dimension_metrics"
}

func UpsertPerfDimensionMetric(metric *PerfDimensionMetric) error {
	if metric == nil || metric.RequestCount == 0 {
		return nil
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "dimension"},
			{Name: "entity_id"},
			{Name: "bucket_ts"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"entity_name":          metric.EntityName,
			"request_count":        gorm.Expr("perf_dimension_metrics.request_count + ?", metric.RequestCount),
			"success_count":        gorm.Expr("perf_dimension_metrics.success_count + ?", metric.SuccessCount),
			"cache_eligible_count": gorm.Expr("perf_dimension_metrics.cache_eligible_count + ?", metric.CacheEligibleCount),
			"cache_hit_count":      gorm.Expr("perf_dimension_metrics.cache_hit_count + ?", metric.CacheHitCount),
			"input_tokens":         gorm.Expr("perf_dimension_metrics.input_tokens + ?", metric.InputTokens),
			"cached_tokens":        gorm.Expr("perf_dimension_metrics.cached_tokens + ?", metric.CachedTokens),
		}),
	}).Create(metric).Error
}

func GetPerfDimensionMetrics(dimension string, startTs int64, endTs int64) ([]PerfDimensionMetric, error) {
	var metrics []PerfDimensionMetric
	err := DB.Model(&PerfDimensionMetric{}).
		Where("dimension = ? AND bucket_ts >= ? AND bucket_ts <= ?", dimension, startTs, endTs).
		Order("bucket_ts ASC, entity_id ASC").
		Find(&metrics).Error
	return metrics, err
}

func DeletePerfDimensionMetricsBefore(cutoffTs int64) error {
	if cutoffTs <= 0 {
		return nil
	}
	return DB.Where("bucket_ts < ?", cutoffTs).Delete(&PerfDimensionMetric{}).Error
}

func PerfDimensionMetricStartTime(hours int) int64 {
	if hours <= 0 {
		hours = 24
	}
	return time.Now().Add(-time.Duration(hours) * time.Hour).Unix()
}
