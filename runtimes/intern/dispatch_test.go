package intern

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.autonomous.ai/os/system/lib/internbridge"
)

type dispatchTransport func(*http.Request) (*http.Response, error)

func (f dispatchTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const syntheticDispatchToken = "synthetic-intern-test-only"

func dispatchFixture(t *testing.T, f dispatchTransport) *substrateDispatcher {
	t.Helper()
	d, err := NewServiceDispatcher(DispatchConfig{Endpoint: CanonicalAuthorityURL, Principal: "intern@gus", TokenSource: func(context.Context) (string, error) { return syntheticDispatchToken, nil }})
	if err != nil {
		t.Fatal(err)
	}
	s := d.(*substrateDispatcher)
	s.http.Transport = f
	return s
}

func serviceProposal(destination string) (internbridge.Request, internbridge.Result) {
	r := internbridge.Request{RunID: "intern-test-1", Text: "public briefing", Operation: internbridge.Reception, DataClass: internbridge.Public}
	intent := "briefing"
	if destination == "pam@gus" {
		r.Text = "remind about business report"
		intent = "reminder"
	}
	h := sha256.Sum256([]byte(r.RunID))
	return r, internbridge.Result{RunID: "run-" + hex.EncodeToString(h[:12]), Destination: internbridge.FirstContact, RequestedDestination: destination,
		Kind: "service", Status: "service_route", Output: "Pending service review", ReceptionRoute: internbridge.ReceptionRoute{Handoff: destination, Intent: intent}}
}

func enqueueAck(id, destination, status string) string {
	return fmt.Sprintf(`{"ok":true,"contract_version":"gus.comms/v1","message_id":"bus-message-1","task_id":%q,"request_id":%q,"status":%q,"destination":%q}`, id, id, status, destination)
}

func authorityResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestDispatchCredentialsFailClosed(t *testing.T) {
	t.Setenv("GUS_INTERN_DISPATCH_TOKEN", "")
	if s := New(); s.dispatcher != nil {
		t.Fatal("default dispatcher present")
	}
	if d, err := NewServiceDispatcher(DispatchConfig{}); err != nil || d != nil {
		t.Fatal("missing source enabled dispatch")
	}
	for _, source := range []func(context.Context) (string, error){
		func(context.Context) (string, error) { return "", nil },
		func(context.Context) (string, error) { return "", errors.New(syntheticDispatchToken) },
	} {
		d := dispatchFixture(t, func(*http.Request) (*http.Response, error) {
			t.Fatal("network used without credential")
			return nil, nil
		})
		d.tokenSource = source
		r, p := serviceProposal("mcavoy@lab")
		got, err := d.Dispatch(context.Background(), r, p)
		if got != nil || !errors.Is(err, internbridge.ErrServiceRoute) || strings.Contains(err.Error(), syntheticDispatchToken) {
			t.Fatal("unsafe missing credential result")
		}
	}
}

func TestDispatchCanonicalEndpoint(t *testing.T) {
	for _, endpoint := range []string{"http://localhost:7370", CanonicalAuthorityURL + "/", CanonicalAuthorityURL + "/dispatch", CanonicalAuthorityURL + "?x=y", "http://user:password@100.115.27.81:7370", "https://100.115.27.81:7370", CanonicalAuthorityURL + "#fragment"} {
		_, err := NewServiceDispatcher(DispatchConfig{Endpoint: endpoint, Principal: "intern@gus", TokenSource: func(context.Context) (string, error) { t.Fatal("resolved during construction"); return "", nil }})
		if !errors.Is(err, ErrDispatchConfig) {
			t.Fatal("noncanonical endpoint accepted")
		}
	}
}

func TestDispatchPrincipalAndDefaults(t *testing.T) {
	for _, principal := range []string{"", "fleet-dispatch@dru", "fleet-dispatch@gus", "fleet-dispatch@lab", "orchestration@gus", "intern@lab", "intern@gus ", "INTERN@gus"} {
		_, err := NewServiceDispatcher(DispatchConfig{Principal: principal, TokenSource: func(context.Context) (string, error) { t.Fatal("credential resolved"); return "", nil }})
		if err != ErrDispatchConfig {
			t.Fatalf("principal %q: %v", principal, err)
		}
	}
	d, err := NewServiceDispatcher(DispatchConfig{Principal: "intern@gus", TokenSource: func(context.Context) (string, error) { return syntheticDispatchToken, nil }})
	if err != nil || d.(*substrateDispatcher).endpoint != CanonicalAuthorityURL {
		t.Fatal("default authority changed")
	}
	for _, cfg := range []DispatchConfig{{Endpoint: CanonicalAuthorityURL + "/messages/post-task"}, {Principal: "fleet-dispatch@gus"}} {
		if _, err := NewServiceDispatcher(cfg); err != ErrDispatchConfig {
			t.Fatal("invalid disabled configuration accepted")
		}
	}
}

func TestDispatchIntentAdmission(t *testing.T) {
	for _, tc := range []struct{ text, intent, destination string }{
		{"news today", "news", "mcavoy@lab"}, {"public headlines", "news", "mcavoy@lab"}, {"public briefing", "briefing", "mcavoy@lab"},
		{"notify the team", "notification", "pam@gus"}, {"notification for meeting", "notification", "pam@gus"},
		{"alarm for meeting", "alarm", "pam@gus"}, {"remind about report", "reminder", "pam@gus"}, {"reminder for meeting", "service", "pam@gus"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			r, p := serviceProposal(tc.destination)
			r.Text = tc.text
			p.ReceptionRoute.Intent = tc.intent
			calls := 0
			d := dispatchFixture(t, func(*http.Request) (*http.Response, error) {
				calls++
				return authorityResponse(202, enqueueAck(r.RunID, tc.destination, "queued")), nil
			})
			if _, err := d.Dispatch(context.Background(), r, p); err != nil || calls != 1 {
				t.Fatalf("admitted intent: %v", err)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*internbridge.Request, *internbridge.Result)
		want   error
	}{
		{"mixed", func(r *internbridge.Request, p *internbridge.Result) { r.Text = "remind me of news" }, internbridge.ErrCustodyHold},
		{"mixed PAM", func(r *internbridge.Request, p *internbridge.Result) { r.Text = "alarm and notification" }, internbridge.ErrCustodyHold},
		{"unknown intent", func(r *internbridge.Request, p *internbridge.Result) { r.Text = "do something" }, internbridge.ErrCustodyHold},
		{"wrong destination", func(r *internbridge.Request, p *internbridge.Result) { p.RequestedDestination = "pam@gus" }, internbridge.ErrProtocol},
		{"wrong label", func(r *internbridge.Request, p *internbridge.Result) { p.ReceptionRoute.Intent = "unknown" }, internbridge.ErrProtocol},
		{"persona label", func(r *internbridge.Request, p *internbridge.Result) { p.ReceptionRoute.Intent = "engineering" }, internbridge.ErrProtocol},
		{"persona text", func(r *internbridge.Request, p *internbridge.Result) { r.Text = "rex public briefing" }, internbridge.ErrCustodyHold},
		{"peer", func(r *internbridge.Request, p *internbridge.Result) { r.Text = "news to worker@lab" }, internbridge.ErrCustodyHold},
		{"fanout", func(r *internbridge.Request, p *internbridge.Result) { r.Text = "broadcast news to peers" }, internbridge.ErrCustodyHold},
		{"private", func(r *internbridge.Request, p *internbridge.Result) { r.DataClass = "private" }, internbridge.ErrInvalidRequest},
		{"restricted", func(r *internbridge.Request, p *internbridge.Result) { r.DataClass = internbridge.Restricted }, internbridge.ErrCustodyHold},
		{"secret", func(r *internbridge.Request, p *internbridge.Result) { r.DataClass = internbridge.Secret }, internbridge.ErrCustodyHold},
		{"unknown class", func(r *internbridge.Request, p *internbridge.Result) { r.DataClass = internbridge.Unknown }, internbridge.ErrNeedsClassification},
		{"home", func(r *internbridge.Request, p *internbridge.Result) { r.Text = "briefing and turn on lights" }, internbridge.ErrCustodyHold},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := dispatchFixture(t, func(*http.Request) (*http.Response, error) {
				t.Fatal("rejected request used transport")
				return nil, nil
			})
			d.tokenSource = func(context.Context) (string, error) { t.Fatal("rejected request resolved credential"); return "", nil }
			r, p := serviceProposal("mcavoy@lab")
			tc.mutate(&r, &p)
			if got, err := d.Dispatch(context.Background(), r, p); got != nil || !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestDispatchStrictAcknowledgement(t *testing.T) {
	valid := enqueueAck("intern-test-1", "mcavoy@lab", "queued")
	for _, body := range []string{valid, strings.Replace(valid, `,"request_id":"intern-test-1"`, "", 1), strings.Replace(valid, `"task_id":"intern-test-1",`, "", 1), strings.TrimSuffix(valid, "}") + `,"delivered":false}`} {
		d := dispatchFixture(t, func(*http.Request) (*http.Response, error) { return authorityResponse(202, body), nil })
		r, p := serviceProposal("mcavoy@lab")
		if _, err := d.Dispatch(context.Background(), r, p); err != nil {
			t.Fatalf("valid ack: %v", err)
		}
	}
	for _, field := range []string{"ok", "contract_version", "message_id", "status", "destination", "task_id", "request_id"} {
		for _, replacement := range []any{nil, false, true, 17, []string{"queued"}, map[string]any{"x": 1}, "wrong", ""} {
			var obj map[string]any
			_ = json.Unmarshal([]byte(valid), &obj)
			obj[field] = replacement
			raw, _ := json.Marshal(obj)
			if field == "ok" && replacement == true {
				continue
			}
			if field == "message_id" && replacement == "wrong" {
				continue // Opaque authority ID; any bounded valid ID is accepted.
			}
			d := dispatchFixture(t, func(*http.Request) (*http.Response, error) { return authorityResponse(200, string(raw)), nil })
			r, p := serviceProposal("mcavoy@lab")
			if got, err := d.Dispatch(context.Background(), r, p); got != nil || err != ErrDispatch {
				t.Fatalf("accepted invalid field %s", field)
			}
		}
		if field == "task_id" || field == "request_id" {
			continue
		}
		var obj map[string]any
		_ = json.Unmarshal([]byte(valid), &obj)
		delete(obj, field)
		raw, _ := json.Marshal(obj)
		d := dispatchFixture(t, func(*http.Request) (*http.Response, error) { return authorityResponse(200, string(raw)), nil })
		r, p := serviceProposal("mcavoy@lab")
		if got, err := d.Dispatch(context.Background(), r, p); got != nil || err != ErrDispatch {
			t.Fatalf("accepted missing %s", field)
		}
	}
	for _, body := range []string{
		strings.Replace(valid, `"queued"`, `"REPORTED_COMPLETE"`, 1), strings.Replace(valid, `"queued"`, `"delivered"`, 1),
		strings.TrimSuffix(valid, "}") + `,"delivered":true}`, strings.TrimSuffix(valid, "}") + `,"delivered":null}`,
		strings.TrimSuffix(valid, "}") + `,"content":"` + syntheticDispatchToken + `"}`, strings.TrimSuffix(valid, "}") + `,"metadata":{"x":1,"x":2}}`,
		strings.TrimSuffix(valid, "}") + `,"status":"queued"}`, strings.TrimSuffix(valid, "}") + `,"st\u0061tus":"queued"}`, valid + ` {}`, valid + `garbage`,
	} {
		d := dispatchFixture(t, func(*http.Request) (*http.Response, error) { return authorityResponse(200, body), nil })
		r, p := serviceProposal("mcavoy@lab")
		if got, err := d.Dispatch(context.Background(), r, p); got != nil || err != ErrDispatch {
			t.Fatal("unsafe acknowledgement accepted")
		}
	}
}

func TestDispatchReplayAndConflict(t *testing.T) {
	var bodies []string
	r, p := serviceProposal("mcavoy@lab")
	d := dispatchFixture(t, func(req *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(req.Body)
		bodies = append(bodies, string(raw))
		return authorityResponse(202, enqueueAck(r.RunID, p.RequestedDestination, "queued")), nil
	})
	for i := 0; i < 2; i++ {
		if i == 1 {
			time.Sleep(time.Second)
		} // Cross a Unix-second boundary: timestamp must stay fixed.
		if _, err := d.Dispatch(context.Background(), r, p); err != nil {
			t.Fatal(err)
		}
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] {
		t.Fatal("replay envelope changed")
	}
	r.Text = "another briefing"
	if _, err := d.Dispatch(context.Background(), r, p); err != internbridge.ErrProtocol || len(bodies) != 2 {
		t.Fatal("conflicting replay sent")
	}
	r, p = serviceProposal("mcavoy@lab")
	r.DataClass = internbridge.Business
	if _, err := d.Dispatch(context.Background(), r, p); err != internbridge.ErrProtocol || len(bodies) != 2 {
		t.Fatal("classification changed on replay")
	}
	d.requests = make(map[string]dispatchReservation)
	for i := 0; i < maxDispatchReservations; i++ {
		d.requests[fmt.Sprint(i)] = dispatchReservation{}
	}
	if _, err := d.Dispatch(context.Background(), r, p); err != internbridge.ErrServiceRoute || len(bodies) != 2 {
		t.Fatal("capacity evicted a reservation")
	}
}

func TestDispatchConcurrentReplayAndAmbiguousFailure(t *testing.T) {
	r, p := serviceProposal("mcavoy@lab")
	var calls atomic.Int32
	var mu sync.Mutex
	var first string
	d := dispatchFixture(t, func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		raw, _ := io.ReadAll(req.Body)
		mu.Lock()
		if first == "" {
			first = string(raw)
		}
		if string(raw) != first {
			t.Error("concurrent replay changed envelope")
		}
		mu.Unlock()
		return nil, errors.New(syntheticDispatchToken)
	})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, err := d.Dispatch(context.Background(), r, p); got != nil || err != ErrDispatch {
				t.Error("unsafe ambiguous result")
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 8 || len(d.requests) != 1 {
		t.Fatal("retry or duplicate reservation")
	}
}

func TestDispatchTransportLimits(t *testing.T) {
	t.Run("bounded envelope", func(t *testing.T) {
		r, p := serviceProposal("mcavoy@lab")
		r.Text = "briefing " + strings.Repeat("é", 7990)
		if err := internbridge.ValidateServiceDispatch(r); err != nil {
			t.Fatalf("input should fit bridge bound: %v", err)
		}
		d := dispatchFixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("oversized envelope sent"); return nil, nil })
		if _, err := d.Dispatch(context.Background(), r, p); err != internbridge.ErrInvalidRequest {
			t.Fatalf("envelope size: %v", err)
		}
	})
	for _, token := range []string{"bad token", "bad\r\nheader", strings.Repeat("x", 8193)} {
		d := dispatchFixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("invalid bearer sent"); return nil, nil })
		d.tokenSource = func(context.Context) (string, error) { return token, nil }
		r, p := serviceProposal("mcavoy@lab")
		if _, err := d.Dispatch(context.Background(), r, p); err != ErrDispatchConfig {
			t.Fatal("invalid bearer accepted")
		}
	}
	for _, header := range []http.Header{{"Content-Type": {"text/plain"}}, {"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}}, {}} {
		d := dispatchFixture(t, func(*http.Request) (*http.Response, error) {
			resp := authorityResponse(200, enqueueAck("intern-test-1", "mcavoy@lab", "queued"))
			resp.Header = header
			return resp, nil
		})
		r, p := serviceProposal("mcavoy@lab")
		if _, err := d.Dispatch(context.Background(), r, p); err != ErrDispatch {
			t.Fatal("unsafe headers accepted")
		}
	}
	d := dispatchFixture(t, func(req *http.Request) (*http.Response, error) {
		if deadline, ok := req.Context().Deadline(); !ok || time.Until(deadline) > internbridge.RequestTimeout {
			t.Error("missing timeout")
		}
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	r, p := serviceProposal("mcavoy@lab")
	if _, err := d.Dispatch(ctx, r, p); err != ErrDispatch {
		t.Fatal("ambiguous timeout taxonomy changed")
	}
	if _, err := d.Dispatch(nil, r, p); err != internbridge.ErrInvalidRequest {
		t.Fatal("nil context taxonomy changed")
	}
}

func TestDispatchPayloadHeadersAndReceipt(t *testing.T) {
	for _, destination := range []string{"mcavoy@lab", "pam@gus"} {
		t.Run(destination, func(t *testing.T) {
			r, p := serviceProposal(destination)
			if destination == "pam@gus" {
				r.Text = "remind about business report"
				r.DataClass = internbridge.Business
			}
			calls := 0
			d := dispatchFixture(t, func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != "POST" || req.URL.String() != CanonicalAuthorityURL+"/messages/post-task" || req.Header.Get("Authorization") != "Bearer "+syntheticDispatchToken || req.Header.Get("X-GUS-Principal") != "intern@gus" || req.Header.Get("Content-Type") != "application/json" {
					t.Error("incorrect request contract")
				}
				var body map[string]any
				if json.NewDecoder(req.Body).Decode(&body) != nil {
					t.Fatal("bad payload")
				}
				if len(body) != 6 || body["request_id"] != r.RunID || body["role"] != "intern@gus" || body["node"] != "gus" || body["to_role"] != destination {
					t.Error("incorrect payload fields")
				}
				at := body["requested_at"].(float64)
				if at != float64(int64(at)) || at > float64(time.Now().Unix()) || at < float64(time.Now().Unix()-5) {
					t.Error("invalid request timestamp")
				}
				hash := sha256.Sum256([]byte(r.RunID))
				want := map[string]any{"schema_version": "gus-bus-task/v1", "task_id": r.RunID, "data_zone": "business", "custody_policy": "business-only", "instruction_inert": true,
					"body": map[string]any{"service_intent": p.ReceptionRoute.Intent, "destination": destination, "request_text": r.Text, "idempotency_key": "intern-" + hex.EncodeToString(hash[:])}}
				if !reflect.DeepEqual(body["task"], want) {
					t.Error("incorrect task envelope")
				}
				return authorityResponse(200, enqueueAck(r.RunID, destination, "accepted")), nil
			})
			got, err := d.Dispatch(context.Background(), r, p)
			if err != nil || got == nil {
				t.Fatal("valid authority result rejected")
			}
			if calls != 1 || got.RunID != r.RunID || got.TaskID != r.RunID || got.BridgeRunID != p.RunID || got.Delivered || got.Status != "accepted" {
				t.Fatal("incorrect receipt")
			}
			raw, _ := json.Marshal(got)
			if strings.Contains(string(raw), syntheticDispatchToken) {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestDispatchCustodyAndCorrelation(t *testing.T) {
	for _, dest := range []string{"smart-home", "unknown", "rex@dru", "cassi@mama"} {
		d := dispatchFixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("held route reached network"); return nil, nil })
		r, p := serviceProposal(dest)
		if _, err := d.Dispatch(context.Background(), r, p); !errors.Is(err, internbridge.ErrCustodyHold) {
			t.Fatal("destination not held")
		}
	}
	for _, mutate := range []func(*internbridge.Request, *internbridge.Result){
		func(r *internbridge.Request, p *internbridge.Result) { r.Text = "turn on lights" },
		func(r *internbridge.Request, p *internbridge.Result) { r.DataClass = internbridge.Unknown },
		func(r *internbridge.Request, p *internbridge.Result) { r.DataClass = internbridge.Secret },
		func(r *internbridge.Request, p *internbridge.Result) { p.RunID = "run-wrong" },
		func(r *internbridge.Request, p *internbridge.Result) { p.ReceptionRoute.Executed = true },
		func(r *internbridge.Request, p *internbridge.Result) { r.Text = syntheticDispatchToken },
	} {
		d := dispatchFixture(t, func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid request reached network")
			return nil, nil
		})
		r, p := serviceProposal("mcavoy@lab")
		mutate(&r, &p)
		if got, err := d.Dispatch(context.Background(), r, p); err == nil || got != nil {
			t.Fatal("invalid request accepted")
		}
	}
}

func TestDispatchMalformedFailureAndSecretLeakage(t *testing.T) {
	for _, status := range []string{"completed", "complete", "delivered", "REPORTED_COMPLETE", "success", "failed"} {
		d := dispatchFixture(t, func(*http.Request) (*http.Response, error) {
			return authorityResponse(200, fmt.Sprintf(`{"ok":true,"status":%q,"task_id":"intern-test-1","worker":"inference@lab"}`, status)), nil
		})
		r, p := serviceProposal("mcavoy@lab")
		if got, err := d.Dispatch(context.Background(), r, p); got != nil || !errors.Is(err, ErrDispatch) {
			t.Fatal("non-acknowledgement status accepted")
		}
	}
	for _, body := range []string{`{"ok":true,"status":"accepted","task_id":"intern-test-1","delivered":true}`, `{"ok":true,"status":"accepted","task_id":"intern-test-1","delivered":null}`, "{\"invalid\":\"\xff\"}"} {
		d := dispatchFixture(t, func(*http.Request) (*http.Response, error) { return authorityResponse(200, body), nil })
		r, p := serviceProposal("mcavoy@lab")
		if got, err := d.Dispatch(context.Background(), r, p); got != nil || !errors.Is(err, ErrDispatch) {
			t.Fatal("contradictory or malformed response accepted")
		}
	}
	for _, body := range []string{`{`, `null`, `{}`, `{"ok":false,"status":"queued","task_id":"intern-test-1"}`, `{"ok":true,"status":"delivered","task_id":"intern-test-1"}`, `{"ok":true,"status":"queued","task_id":"wrong"}`, `{"ok":true,"ok":false,"status":"queued","task_id":"intern-test-1"}`, `{"ok":true,"status":"queued","task_id":"intern-test-1"} {}`, `{"ok":true,"status":"REPORTED_COMPLETE","task_id":"intern-test-1","worker":"inference@mama"}`, strings.Repeat("x", internbridge.MaxResponseBytes+1)} {
		d := dispatchFixture(t, func(*http.Request) (*http.Response, error) { return authorityResponse(200, body), nil })
		r, p := serviceProposal("mcavoy@lab")
		if got, err := d.Dispatch(context.Background(), r, p); got != nil || !errors.Is(err, ErrDispatch) {
			t.Fatal("invalid response accepted")
		}
	}
	for _, code := range []int{301, 401, 403, 500, 503} {
		d := dispatchFixture(t, func(*http.Request) (*http.Response, error) {
			return authorityResponse(code, syntheticDispatchToken), nil
		})
		r, p := serviceProposal("mcavoy@lab")
		got, err := d.Dispatch(context.Background(), r, p)
		if got != nil || !errors.Is(err, ErrDispatch) || strings.Contains(err.Error(), syntheticDispatchToken) {
			t.Fatal("unsafe failed response")
		}
	}
	d := dispatchFixture(t, func(*http.Request) (*http.Response, error) { return nil, errors.New(syntheticDispatchToken) })
	r, p := serviceProposal("mcavoy@lab")
	_, err := d.Dispatch(context.Background(), r, p)
	if err == nil || strings.Contains(err.Error(), syntheticDispatchToken) {
		t.Fatal("unsafe transport error")
	}
}

func TestDispatchRuntimeConfigurationAndCancellation(t *testing.T) {
	t.Setenv("GUS_INTERN_DISPATCH_TOKEN", syntheticDispatchToken)
	t.Setenv("GUS_INTERN_DISPATCH_PRINCIPAL", "unassigned")
	if New().dispatcher != nil {
		t.Fatal("invalid principal enabled default dispatcher")
	}
	t.Setenv("GUS_INTERN_DISPATCH_PRINCIPAL", "intern@gus")
	t.Setenv("GUS_INTERN_DISPATCH_URL", "http://127.0.0.1:7370")
	if New().dispatcher != nil {
		t.Fatal("invalid URL enabled default dispatcher")
	}
	t.Setenv("GUS_INTERN_DISPATCH_URL", CanonicalAuthorityURL)
	s := New()
	if s.dispatcher == nil {
		t.Fatal("explicit environment configuration not wired")
	}
	d := s.dispatcher.(*substrateDispatcher)
	d.http.Transport = dispatchTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("network after credential removal or cancellation")
		return nil, nil
	})
	r, p := serviceProposal("pam@gus")
	t.Setenv("GUS_INTERN_DISPATCH_TOKEN", "")
	if _, err := d.Dispatch(context.Background(), r, p); !errors.Is(err, internbridge.ErrServiceRoute) {
		t.Fatal("credential removal not fail closed")
	}
	t.Setenv("GUS_INTERN_DISPATCH_TOKEN", syntheticDispatchToken)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Dispatch(ctx, r, p); !errors.Is(err, internbridge.ErrCanceled) {
		t.Fatal("canceled dispatch accepted")
	}
}

func TestDispatchRedirectNoForward(t *testing.T) {
	var calls atomic.Int32
	d := dispatchFixture(t, func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		resp := authorityResponse(302, "")
		resp.Header.Set("Location", "http://redirect.invalid/dispatch")
		return resp, nil
	})
	r, p := serviceProposal("pam@gus")
	if _, err := d.Dispatch(context.Background(), r, p); err == nil {
		t.Fatal("redirect accepted")
	}
	if calls.Load() != 1 {
		t.Fatal("redirect forwarded")
	}
}

func TestServiceDispatchIntegration(t *testing.T) {
	for _, configured := range []bool{false, true} {
		for _, destination := range []string{"mcavoy@lab", "pam@gus", "rex@dru"} {
			t.Run(fmt.Sprintf("%t/%s", configured, destination), func(t *testing.T) {
				s := bridgeFixture(t, func(w http.ResponseWriter, r *http.Request) {
					var req internbridge.Request
					_ = json.NewDecoder(r.Body).Decode(&req)
					body := envelope(req)
					if destination != "rex@dru" {
						body["requested_destination"] = destination
						body["kind"] = "service"
						body["status"] = "service_route"
						body["reception_route"].(map[string]any)["handoff"] = destination
						intent := "briefing"
						if destination == "pam@gus" {
							intent = "reminder"
						}
						body["reception_route"].(map[string]any)["intent"] = intent
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(body)
				})
				var calls atomic.Int32
				if configured {
					s.dispatcher = dispatchFixture(t, func(r *http.Request) (*http.Response, error) {
						calls.Add(1)
						var body map[string]any
						_ = json.NewDecoder(r.Body).Decode(&body)
						return authorityResponse(202, enqueueAck(body["request_id"].(string), destination, "queued")), nil
					})
				}
				startWorker(t, s)
				input := "public briefing"
				if destination == "pam@gus" {
					input = "remind about business report"
				}
				id, err := s.Submit(admitted(t, input))
				if err != nil {
					t.Fatal(err)
				}
				var turn Turn
				until(t, func() bool {
					turn, _ = s.Result(id)
					return turn.Dispatch != nil || turn.State == "failed" || turn.State == "completed"
				})
				if destination == "rex@dru" {
					if calls.Load() != 0 || turn.State != "completed" || turn.Dispatch != nil {
						t.Fatal("persona dispatched")
					}
					return
				}
				if !configured {
					if turn.Error != internbridge.ErrServiceRoute.Error() || turn.Result != nil {
						t.Fatal("missing dispatcher not fail closed")
					}
					return
				}
				if calls.Load() != 1 || turn.State != "queued" || turn.Scope != "service_dispatch" || turn.Dispatch.RunID != id || turn.Dispatch.Delivered || turn.ExecutesActions {
					t.Fatal("invalid service receipt")
				}
				turn.Dispatch.Delivered = true
				again, _ := s.Result(id)
				if again.Dispatch.Delivered {
					t.Fatal("receipt aliases stored result")
				}
			})
		}
	}
}
