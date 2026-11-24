package notificationhttp

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"rental-management/internal/adapter/http/httpx"
	domain "rental-management/internal/domain/notification"
	usecase "rental-management/internal/usecase/notification"
)

// PopupHandler serves the popups of the signed in person: which ones to show, and what they did with them.
type PopupHandler struct {
	uc   *usecase.Popups
	urls FileURLs
}

// NewPopupHandler returns the handler.
func NewPopupHandler(uc *usecase.Popups, urls FileURLs) *PopupHandler {
	return &PopupHandler{uc: uc, urls: urls}
}

type popupImageView struct {
	ID      uuid.UUID `json:"id"`
	URL     string    `json:"url"`
	Width   int       `json:"width"`
	Height  int       `json:"height"`
	LinkURL *string   `json:"link_url"`
}

type popupFromView struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Role string `json:"role"`
}

// popupView is a popup as the person it is shown to sees it.
type popupView struct {
	ID        uuid.UUID         `json:"id"`
	Type      string            `json:"type"`
	Title     map[string]string `json:"title"`
	Body      map[string]string `json:"body"`
	CTA       *ctaView          `json:"cta"`
	PopupSize string            `json:"popup_size"`
	Images    []popupImageView  `json:"images"`
	Revision  int               `json:"revision"`
	From      popupFromView     `json:"from"`
}

func popupViewOf(detail domain.AnnouncementDetail, urls FileURLs) popupView {
	view := popupView{
		ID: detail.ID, Type: string(detail.Type), Title: textsView(detail.Title), Body: textsView(detail.Body),
		PopupSize: string(detail.PopupSize), Images: make([]popupImageView, 0, len(detail.Images)), Revision: detail.Revision,
		From: popupFromView{Kind: string(detail.SenderKind()), Role: detail.SenderRole},
	}
	if detail.CreatedBy != nil {
		view.From.Name = detail.CreatedBy.FullName
	}
	if detail.CTAURL != "" {
		view.CTA = &ctaView{Label: textsView(detail.CTALabel), URL: detail.CTAURL}
	}
	for _, img := range detail.Images {
		v := popupImageView{ID: img.ID, URL: urls.URL(img.FileKey), Width: img.Width, Height: img.Height}
		if img.LinkURL != "" {
			v.LinkURL = &img.LinkURL
		}
		view.Images = append(view.Images, v)
	}
	return view
}

// Active answers GET /v1/popups/active: at most three popups to show now, most important first.
func (handler *PopupHandler) Active(ginCtx *gin.Context) {
	viewer, ok := viewerOf(ginCtx)
	if !ok {
		return
	}
	popups, err := handler.uc.Active(ginCtx.Request.Context(), usecase.PopupViewer{UserID: viewer.UserID, SessionID: sessionOf(ginCtx)})
	if err != nil {
		httpx.Fail(ginCtx, mapError(err))
		return
	}
	views := make([]popupView, 0, len(popups))
	for _, detail := range popups {
		views = append(views, popupViewOf(detail, handler.urls))
	}
	httpx.OK(ginCtx, views)
}

// Record answers POST /v1/popups/{id}/{seen|dismiss|click}.
func (handler *PopupHandler) Record(action domain.ImpressionAction) gin.HandlerFunc {
	return func(ginCtx *gin.Context) {
		viewer, ok := viewerOf(ginCtx)
		if !ok {
			return
		}
		id, err := httpx.ParseUUIDParam(ginCtx, "id")
		if err != nil {
			httpx.Fail(ginCtx, err)
			return
		}
		err = handler.uc.Record(ginCtx.Request.Context(), usecase.PopupViewer{UserID: viewer.UserID, SessionID: sessionOf(ginCtx)}, id, action)
		if err != nil {
			httpx.Fail(ginCtx, mapError(err))
			return
		}
		httpx.NoContent(ginCtx)
	}
}
