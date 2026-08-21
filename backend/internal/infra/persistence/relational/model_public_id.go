package relational

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
	modeldomain "github.com/chenyme/grok2api/backend/internal/domain/model"
	"github.com/chenyme/grok2api/backend/internal/repository"
	"gorm.io/gorm"
)

type modelPublicIDMigration struct {
	ID       uint64
	Previous string
	Current  string
}

// ensureCanonicalModelPublicIDs 原位迁移内部路由 ID，保留路由主键和所有下游授权关系。
func (d *Database) ensureCanonicalModelPublicIDs(ctx context.Context) error {
	return d.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []modelRouteModel
		if err := tx.Order("id ASC").Find(&rows).Error; err != nil {
			return err
		}
		migrations := make([]modelPublicIDMigration, 0, len(rows))
		for _, row := range rows {
			providerValue := account.Provider(row.Provider)
			publicID, ok := modeldomain.NormalizePublicID(providerValue, row.PublicID)
			if !ok {
				return fmt.Errorf("ID awam %q bagi laluan model %d tidak dapat dinormalkan kepada %s", row.PublicID, row.ID, providerValue)
			}
			if publicID == row.PublicID {
				continue
			}
			migrations = append(migrations, modelPublicIDMigration{ID: row.ID, Previous: row.PublicID, Current: publicID})
		}
		for _, migration := range migrations {
			if err := ensureModelPublicIDNotAlias(tx, migration.Current, migration.ID); err != nil {
				return err
			}
			if err := preserveModelRouteAlias(tx, migration.Previous, migration.ID); err != nil {
				return err
			}
		}
		// 全部先移到不冲突的临时命名空间，允许 A/B 两条路由交换或释放目标名称。
		for _, migration := range migrations {
			temporary := fmt.Sprintf("__grok2api_model_namespace_%d", migration.ID)
			if err := tx.Model(&modelRouteModel{}).Where("id = ?", migration.ID).Update("public_id", temporary).Error; err != nil {
				return mapError(err)
			}
		}
		for _, migration := range migrations {
			if err := tx.Model(&modelRouteModel{}).Where("id = ?", migration.ID).Update("public_id", migration.Current).Error; err != nil {
				return fmt.Errorf("Memigrasikan laluan model %d ke %q: %w", migration.ID, migration.Current, mapError(err))
			}
		}
		return nil
	})
}

func preserveModelRouteAlias(tx *gorm.DB, alias string, routeID uint64) error {
	alias = strings.TrimSpace(alias)
	if alias == "" || routeID == 0 {
		return nil
	}
	var route modelRouteModel
	if err := tx.Where("public_id = ? AND id <> ?", alias, routeID).First(&route).Error; err == nil {
		// Another target still owns the old public name, so the group remains
		// reachable without creating a compatibility alias for this renamed target.
		return nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var existing modelRouteAliasModel
	err := tx.Where("alias = ?", alias).First(&existing).Error
	if err == nil {
		if existing.ModelRouteID == routeID {
			return nil
		}
		return fmt.Errorf("%w: Nama serasi model %q telah mengikat laluan %d", repository.ErrConflict, alias, existing.ModelRouteID)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return tx.Create(&modelRouteAliasModel{Alias: alias, ModelRouteID: routeID}).Error
}

func ensureModelPublicIDNotAlias(tx *gorm.DB, publicID string, routeID uint64) error {
	var alias modelRouteAliasModel
	query := tx.Where("alias = ?", publicID)
	if routeID != 0 {
		query = query.Where("model_route_id <> ?", routeID)
	}
	err := query.First(&alias).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: ID awam model %q telah diketepikan sebagai nama serasi oleh laluan %d", repository.ErrConflict, publicID, alias.ModelRouteID)
}
