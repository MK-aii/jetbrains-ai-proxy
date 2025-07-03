package middleware

import (
	"github.com/labstack/echo"
	"jetbrains-ai-proxy/internal/config"
	"log"
	"net/http"
	"strings"
)

func BearerAuth() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// 获取Authorization header
			auth := c.Request().Header.Get("Authorization")

			if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid authorization header")
			}

			token := strings.TrimPrefix(auth, "Bearer ")

			if token != config.GetGlobalConfig().GetConfig().BearerToken || token == "" {
				log.Printf("invalid token: %s", token)
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
			}

			return next(c)
		}
	}
}
