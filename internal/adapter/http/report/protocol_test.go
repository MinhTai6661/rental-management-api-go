package reporthttp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"rental-management/internal/adapter/http/apperr"
	domain "rental-management/internal/domain/report"
	"rental-management/pkg/i18n"
)

func TestParseClientFrame(t *testing.T) {
	id := uuid.New()
	cases := []struct {
		name     string
		in       string
		wantType string
		wantBody string
		wantErr  string // "" for none, else the code
		wantRule string
		wantID   string
	}{
		{name: "reply", in: `{"type":"reply","client_id":"` + id.String() + `","body":"  hi  "}`, wantType: "reply", wantBody: "hi"},
		{name: "typing", in: `{"type":"typing"}`, wantType: "typing"},
		{name: "not json", in: `{nope`, wantErr: apperr.CodeRequestMalformed},
		{name: "unknown type", in: `{"type":"x","client_id":"c"}`, wantErr: apperr.CodeValidationFailed, wantRule: apperr.RuleOneOf, wantID: "c"},
		{name: "no type", in: `{}`, wantErr: apperr.CodeValidationFailed, wantRule: apperr.RuleOneOf},
		{name: "bad client id", in: `{"type":"reply","client_id":"1","body":"x"}`, wantErr: apperr.CodeValidationFailed, wantRule: apperr.RuleUUID},
		{name: "blank body", in: `{"type":"reply","client_id":"` + id.String() + `","body":" "}`, wantErr: apperr.CodeValidationFailed, wantRule: apperr.RuleRequired, wantID: id.String()},
		{name: "long body", in: `{"type":"reply","client_id":"` + id.String() + `","body":"` + strings.Repeat("é", 5001) + `"}`, wantErr: apperr.CodeValidationFailed, wantRule: apperr.RuleMaxString, wantID: id.String()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, clientID, err := parseClientFrame([]byte(c.in))
			if c.wantErr == "" {
				if err != nil || got.Type != c.wantType || got.Body != c.wantBody {
					t.Fatalf("got %+v, %v", got, err)
				}
				return
			}
			if err == nil || err.Code != c.wantErr || clientID != c.wantID {
				t.Fatalf("got %v client %q, want %s client %q", err, clientID, c.wantErr, c.wantID)
			}
			if c.wantRule != "" && (len(err.Fields) != 1 || err.Fields[0].Rule != c.wantRule) {
				t.Fatalf("fields = %+v", err.Fields)
			}
		})
	}
}

func TestErrorFrameIsTranslatedLikeTheHTTPAPI(t *testing.T) {
	catalog, err := i18n.New()
	if err != nil {
		t.Fatal(err)
	}
	en := catalog.Localizer("en")
	var frame errorFrame
	data := errorFrameOf(en, apperr.E(apperr.KindForbidden, apperr.CodeReportReadOnly), "c1")
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Type != "error" || frame.Code != "report.read_only" || frame.ClientID != "c1" || frame.Message == "errors.report.read_only" || frame.Message == "" {
		t.Errorf("frame = %+v", frame)
	}
	data = errorFrameOf(en, apperr.Invalid(apperr.Field{Name: "client_id", Rule: apperr.RuleUUID}), "")
	if err := json.Unmarshal(data, &frame); err != nil || len(frame.Fields["client_id"]) != 1 {
		t.Errorf("fields = %+v, %v", frame.Fields, err)
	}
}

func TestServerFramesCarryTheDocumentedFields(t *testing.T) {
	id, reply := uuid.New(), uuid.New()
	ready := string(readyFrameOf(domain.View{Report: domain.Report{ID: id, Status: domain.StatusOpen}}))
	if !strings.Contains(ready, `"type":"ready"`) || !strings.Contains(ready, `"v":1`) || !strings.Contains(ready, `"deleted":false`) {
		t.Errorf("ready = %s", ready)
	}
	if got := string(ackFrameOf(id, reply)); got != `{"type":"ack","client_id":"`+id.String()+`","reply_id":"`+reply.String()+`"}` {
		t.Errorf("ack = %s", got)
	}
}

func TestFrameBudgetResetsEveryMinute(t *testing.T) {
	now := time.Now()
	budget := frameBudget{limit: 3, now: func() time.Time { return now }}
	for i := range 3 {
		if !budget.allow() {
			t.Fatalf("frame %d refused", i)
		}
	}
	if budget.allow() {
		t.Fatal("the fourth frame is over the budget")
	}
	now = now.Add(61 * time.Second)
	if !budget.allow() {
		t.Fatal("a new minute starts a new budget")
	}
}

func TestOriginAllowedComparesWithTheConfiguredOrigins(t *testing.T) {
	h := &SocketHandler{settings: SocketSettings{AllowedOrigins: []string{"http://localhost:2001", "https://app.example.vn/"}}}
	for origin, want := range map[string]bool{
		"": true, "http://localhost:2001": true, "HTTP://LOCALHOST:2001": true, "https://app.example.vn": true,
		"http://localhost:2002": false, "https://evil.example": false, "http://localhost:2001.evil.example": false, "null": false,
	} {
		if got := h.originAllowed(origin); got != want {
			t.Errorf("origin %q = %v, want %v", origin, got, want)
		}
	}
}
