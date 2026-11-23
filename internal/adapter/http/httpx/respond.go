// Package httpx is the HTTP layer shared by every module: the JSON envelope, error mapping, pagination,
// request validation and middleware. It is the only package (with the app wiring and module handlers/routes)
// that imports gin.
//
// Success bodies:  {"data": ...}  and, for lists, {"data": [...], "meta": {...}}.
// Error bodies:    {"error": {"code": "auth.forbidden", "message": "...", "fields": {"email": ["..."]}}}.
package httpx

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"rental-management/pkg/i18n"
)

// Keys under which middleware stores request-scoped values in the gin context.
const (
	localizerKey = "httpx.localizer"
	loggerKey    = "httpx.logger"
)

type (
	body struct {
		Data any `json:"data"`
	}
	listBody struct {
		Data any  `json:"data"`
		Meta Meta `json:"meta"`
	}
)

// Localize returns the translator chosen for this request by the Locale middleware.
func Localize(c *gin.Context) i18n.Localizer {
	if l, ok := c.Get(localizerKey); ok {
		return l.(i18n.Localizer)
	}
	return i18n.Localizer{} // no catalog: keys come back untranslated
}

// Log returns the request logger (it carries the request id).
func Log(c *gin.Context) *slog.Logger {
	if l, ok := c.Get(loggerKey); ok {
		return l.(*slog.Logger)
	}
	return slog.New(slog.DiscardHandler)
}

// OK writes a 200 response with data under "data".
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, body{Data: data})
}

// Created writes a 201 response with the new resource under "data".
func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, body{Data: data})
}

// NoContent writes a 204 response with no body.
func NoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
	c.Writer.WriteHeaderNow()
}

// Accepted writes a 202 response with no body: the request was understood and its work happens in the background.
func Accepted(c *gin.Context) {
	c.Status(http.StatusAccepted)
	c.Writer.WriteHeaderNow()
}

// List writes a 200 response for a list. Pass a non-nil slice so an empty list is "[]" and not "null".
func List(c *gin.Context, data any, meta Meta) {
	c.JSON(http.StatusOK, listBody{Data: data, Meta: meta})
}
