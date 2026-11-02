package persistence

import "gorm.io/gorm"

// MetricStatModuleModes 返回汇总指标下各统计模块配置的跨子指标合并方式。
func MetricStatModuleModes(db *gorm.DB, aggregateCode string) (map[string]string, error) {
	var rows []MetricStatModule
	if err := db.Where("aggregate_code = ?", aggregateCode).Find(&rows).Error; err != nil {
		return nil, err
	}
	modes := make(map[string]string, len(rows))
	for i := range rows {
		modes[rows[i].ModuleKey] = rows[i].AggMode
	}
	return modes, nil
}
