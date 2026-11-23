package httpx

import (
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
)

// CORS lets the web app origins in allowed call the API from a browser. Tokens travel in the Authorization header,
// never in cookies, so nothing carries credentials and there is no wildcard: an origin that is not listed gets no
// CORS headers (the browser then blocks the call). Preflights are cached for a day so the browser does not send an
// OPTIONS request before every call. It must run first, so error responses carry the headers too.
func CORS(allowed []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			c.Next()
			return
		}
		h := c.Writer.Header()
		h.Add("Vary", "Origin")
		isPreflight := c.Request.Method == http.MethodOptions && c.GetHeader("Access-Control-Request-Method") != ""
		if !slices.Contains(allowed, origin) {
			if isPreflight {
				c.AbortWithStatus(http.StatusNoContent)
				return
			}
			c.Next()
			return
		}
		h.Set("Access-Control-Allow-Origin", origin)
		if !isPreflight {
			h.Set("Access-Control-Expose-Headers", "Content-Disposition")
			c.Next()
			return
		}
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept-Language, X-Request-Id")
		h.Set("Access-Control-Max-Age", "86400")
		h.Add("Vary", "Access-Control-Request-Method")
		h.Add("Vary", "Access-Control-Request-Headers")
		c.AbortWithStatus(http.StatusNoContent)
	}
}
