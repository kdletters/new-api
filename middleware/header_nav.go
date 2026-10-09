package middleware

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type headerNavAccess struct {
	Enabled     bool
	RequireAuth bool
	// Groups restricts the module to the listed account groups. An empty list
	// keeps the module visible to everyone.
	Groups []string
}

func getHeaderNavAccess(module string) headerNavAccess {
	fallback := headerNavAccess{
		Enabled:     true,
		RequireAuth: false,
	}

	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap["HeaderNavModules"]
	common.OptionMapRWMutex.RUnlock()

	if strings.TrimSpace(raw) == "" {
		return fallback
	}

	var parsed map[string]any
	if err := common.Unmarshal([]byte(raw), &parsed); err != nil {
		return fallback
	}

	return parseHeaderNavAccess(parsed[module], fallback)
}

func parseHeaderNavAccess(raw any, fallback headerNavAccess) headerNavAccess {
	switch value := raw.(type) {
	case bool:
		return headerNavAccess{
			Enabled:     value,
			RequireAuth: fallback.RequireAuth,
		}
	case string:
		return headerNavAccess{
			Enabled:     parseHeaderNavBool(value, fallback.Enabled),
			RequireAuth: fallback.RequireAuth,
		}
	case float64:
		return headerNavAccess{
			Enabled:     parseHeaderNavBool(value, fallback.Enabled),
			RequireAuth: fallback.RequireAuth,
		}
	case map[string]any:
		access := fallback
		if enabled, ok := value["enabled"]; ok {
			access.Enabled = parseHeaderNavBool(enabled, fallback.Enabled)
		}
		if requireAuth, ok := value["requireAuth"]; ok {
			access.RequireAuth = parseHeaderNavBool(requireAuth, fallback.RequireAuth)
		}
		if groups, ok := value["groups"]; ok {
			access.Groups = parseHeaderNavGroups(groups)
		}
		return access
	default:
		return fallback
	}
}

func parseHeaderNavBool(value any, fallback bool) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1":
			return true
		case "false", "0":
			return false
		default:
			return fallback
		}
	case float64:
		if v == 1 {
			return true
		}
		if v == 0 {
			return false
		}
		return fallback
	case int:
		if v == 1 {
			return true
		}
		if v == 0 {
			return false
		}
		return fallback
	default:
		return fallback
	}
}

// parseHeaderNavGroups normalizes the optional visible-group allowlist.
// Missing, empty, or malformed values mean the module is open to everyone.
func parseHeaderNavGroups(raw any) []string {
	var candidates []string
	switch value := raw.(type) {
	case []any:
		for _, item := range value {
			if text, ok := item.(string); ok {
				candidates = append(candidates, text)
			}
		}
	case []string:
		candidates = value
	case string:
		candidates = strings.Split(value, ",")
	default:
		return nil
	}

	groups := make([]string, 0, len(candidates))
	seen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		group := strings.TrimSpace(candidate)
		if group == "" || seen[group] {
			continue
		}
		seen[group] = true
		groups = append(groups, group)
	}
	return groups
}

// headerNavGroupAllowed reports whether the current visitor belongs to the
// module allowlist. Authenticated requests carry the account group in the gin
// context; other visitors are never allowed when an allowlist exists.
func headerNavGroupAllowed(c *gin.Context, allowed []string) bool {
	group := strings.TrimSpace(c.GetString("group"))
	if group == "" {
		userID := c.GetInt("id")
		if userID <= 0 {
			return false
		}
		resolved, err := model.GetUserGroup(userID, false)
		if err != nil {
			return false
		}
		group = strings.TrimSpace(resolved)
	}
	if group == "" {
		return false
	}
	return slices.Contains(allowed, group)
}

func writeHeaderNavGroupForbidden(c *gin.Context, module string) {
	c.JSON(http.StatusForbidden, gin.H{
		"success": false,
		"message": fmt.Sprintf("%s is not available for your group", module),
	})
	c.Abort()
}

// VisibleHeaderNavModules returns the header navigation option with modules
// hidden when the current visitor is not allowed to view them, so the frontend
// drops the navigation entry and its route guards redirect away.
func VisibleHeaderNavModules(c *gin.Context) string {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap["HeaderNavModules"]
	common.OptionMapRWMutex.RUnlock()

	if strings.TrimSpace(raw) == "" || !strings.Contains(raw, "\"groups\"") {
		return raw
	}

	var parsed map[string]any
	if err := common.Unmarshal([]byte(raw), &parsed); err != nil {
		return raw
	}

	changed := false
	for module, value := range parsed {
		access := parseHeaderNavAccess(value, headerNavAccess{Enabled: true, RequireAuth: false})
		if !access.Enabled || len(access.Groups) == 0 {
			continue
		}
		if headerNavGroupAllowed(c, access.Groups) {
			continue
		}
		parsed[module] = map[string]any{"enabled": false, "requireAuth": false}
		changed = true
	}
	if !changed {
		return raw
	}

	encoded, err := common.Marshal(parsed)
	if err != nil {
		return raw
	}
	return string(encoded)
}

func HeaderNavModuleAuth(module string) gin.HandlerFunc {
	return func(c *gin.Context) {
		access := getHeaderNavAccess(module)
		if !access.Enabled {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": fmt.Sprintf("%s is disabled", module),
			})
			c.Abort()
			return
		}

		if access.RequireAuth || len(access.Groups) > 0 {
			UserAuth()(c)
			return
		}

		TryUserAuth()(c)
	}
}

// HeaderNavGroupAuth enforces the module visible-group allowlist. It must be
// registered behind HeaderNavModuleAuth (or HeaderNavModulePublicOrUserAuth)
// because both run the remaining chain internally, and because the group
// allowlist requires an authenticated visitor.
func HeaderNavGroupAuth(module string) gin.HandlerFunc {
	return func(c *gin.Context) {
		access := getHeaderNavAccess(module)
		if len(access.Groups) == 0 {
			return
		}
		if !headerNavGroupAllowed(c, access.Groups) {
			writeHeaderNavGroupForbidden(c, module)
		}
	}
}

func HeaderNavModulePublicOrUserAuth(module string) gin.HandlerFunc {
	return func(c *gin.Context) {
		access := getHeaderNavAccess(module)
		if !access.Enabled || access.RequireAuth {
			UserAuth()(c)
			return
		}

		TryUserAuth()(c)
	}
}
