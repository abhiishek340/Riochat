package channel

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// routeRecord is a route joined with its upstream model and upstream.
type routeRecord struct {
	RouteID         uint      `gorm:"column:route_id"`
	PlatformModelID uint      `gorm:"column:platform_model_id"`
	UpstreamModelID uint      `gorm:"column:upstream_model_id"`
	Protocol        string    `gorm:"column:protocol"`
	RouteStatus     string    `gorm:"column:route_status"`
	Priority        int       `gorm:"column:priority"`
	Weight          int       `gorm:"column:weight"`
	RouteSource     string    `gorm:"column:route_source"`
	RouteCBFailure  int       `gorm:"column:route_cb_failure"`
	RouteCBDuration int       `gorm:"column:route_cb_duration"`
	RouteCBWindow   int       `gorm:"column:route_cb_window"`
	RouteHeaders    string    `gorm:"column:route_headers"`
	RouteCreatedAt  time.Time `gorm:"column:route_created_at"`
	RouteUpdatedAt  time.Time `gorm:"column:route_updated_at"`

	UpstreamID        uint   `gorm:"column:upstream_id"`
	BindingCode       string `gorm:"column:binding_code"`
	UpstreamModelName string `gorm:"column:upstream_model_name"`
	UMVendor          string `gorm:"column:um_vendor"`
	UMIcon            string `gorm:"column:um_icon"`
	UMKindsJSON       string `gorm:"column:um_kinds_json"`
	SuggestedProtocol string `gorm:"column:suggested_protocol"`
	UMStatus          string `gorm:"column:um_status"`

	UpName    string `gorm:"column:up_name"`
	UpBaseURL string `gorm:"column:up_base_url"`
	UpStatus  string `gorm:"column:up_status"`
}

// activeChain: route, upstream model and upstream are all active.
func (r routeRecord) activeChain() bool {
	return r.RouteStatus == StatusActive && r.UMStatus == StatusActive && r.UpStatus == StatusActive
}

const routeSelect = `r.id AS route_id, r.platform_model_id AS platform_model_id, r.upstream_model_id AS upstream_model_id,
 r.protocol AS protocol, r.status AS route_status, r.priority AS priority, r.weight AS weight, r.source AS route_source,
 r.cb_failure_threshold AS route_cb_failure, r.cb_duration_min AS route_cb_duration, r.cb_window_min AS route_cb_window,
 r.headers_json AS route_headers, r.created_at AS route_created_at, r.updated_at AS route_updated_at,
 um.upstream_id AS upstream_id, um.binding_code AS binding_code, um.upstream_model_name AS upstream_model_name,
 um.vendor AS um_vendor, um.icon AS um_icon, um.kinds_json AS um_kinds_json, um.suggested_protocol AS suggested_protocol,
 um.status AS um_status, up.name AS up_name, up.base_url AS up_base_url, up.status AS up_status`

// routeQuery starts a query over joined route records.
func routeQuery(db *gorm.DB) *gorm.DB {
	return db.Table("llm_model_routes AS r").Select(routeSelect).
		Joins("JOIN llm_upstream_models um ON um.id = r.upstream_model_id").
		Joins("JOIN llm_upstreams up ON up.id = um.upstream_id")
}

// loadRoutes loads the route records of the given platform models (all when ids is nil).
func (s *Service) loadRoutes(ctx context.Context, ids []uint) ([]routeRecord, error) {
	q := routeQuery(s.db.WithContext(ctx))
	if ids != nil {
		if len(ids) == 0 {
			return nil, nil
		}
		q = q.Where("r.platform_model_id IN ?", ids)
	}
	var out []routeRecord
	err := q.Order("r.priority ASC, r.id ASC").Scan(&out).Error
	return out, err
}

func groupRoutesByModel(rs []routeRecord) map[uint][]routeRecord {
	out := map[uint][]routeRecord{}
	for _, r := range rs {
		out[r.PlatformModelID] = append(out[r.PlatformModelID], r)
	}
	return out
}
