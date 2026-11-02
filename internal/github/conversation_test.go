package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const conversationHunk = "@@ -209,2 +209,5 @@\n \tcase a.drawer.tree:\n-\t\ta.reviewLeaveTree(v)\n+\t\tif k == keyTab {\n+\t\t\treturn a.reviewHideTree(v)\n+\t\t}"

func TestConversation(t *testing.T) {
	t.Parallel()

	rec := &gqlRecorder{body: readFixture(t, "conversation.json")}
	c, reqs := newTestClient(t, "github.com", rec)

	conv, err := c.Conversation(context.Background(), testRepo, 24)
	if err != nil {
		t.Fatal(err)
	}

	if got := reqs(); len(got) != 1 || got[0].Method != http.MethodPost || got[0].URL.String() != "https://api.github.com/graphql" {
		t.Fatalf("requests = %v, want one POST to https://api.github.com/graphql", got)
	}

	var body struct {
		Variables map[string]any `json:"variables"`
	}
	if err := json.Unmarshal([]byte(rec.requests()[0]), &body); err != nil {
		t.Fatal(err)
	}

	wantVars := map[string]any{"owner": "octo", "name": "repo", "number": float64(24), "cursor": nil}
	if !reflect.DeepEqual(body.Variables, wantVars) {
		t.Errorf("variables = %v, want %v", body.Variables, wantVars)
	}

	if conv.PR.Number != 24 || conv.PR.Author != "mhersson" {
		t.Errorf("PR = %+v", conv.PR)
	}

	if want := mustTime(t, "2026-10-29T08:00:00Z"); !conv.Created.Equal(want) {
		t.Errorf("Created = %v, want %v", conv.Created, want)
	}

	if conv.Cut {
		t.Error("Cut = true, want false")
	}

	wantImages := map[string]string{
		"https://github.com/user-attachments/assets/e8b9a952-456e-4a57-b08c-012dbbee61fa": "https://private-user-images.githubusercontent.com/1/301-e8b9a952-456e-4a57-b08c-012dbbee61fa.png?jwt=abc&x=1",
	}
	if !reflect.DeepEqual(conv.Body.Images, wantImages) {
		t.Errorf("Body.Images = %v, want %v", conv.Body.Images, wantImages)
	}

	type brief struct {
		Kind     ItemKind
		Author   string
		Bot      bool
		State    string
		Commits  int
		Reviewer string
		ID       string
	}

	want := []brief{
		{Kind: ItemComment, Author: "codecov[bot]", Bot: true, ID: "IC_1"},
		{Kind: ItemCommits, Author: "mhersson", Commits: 3, ID: "aaa3"},
		{Kind: ItemReview, Author: "kari-dev", State: "CHANGES_REQUESTED", ID: "PRR_1"},
		{Kind: ItemForcePush, Author: "mhersson", ID: "FP_1"},
		{Kind: ItemReviewRequest, Author: "mhersson", Reviewer: "octo-reviewer", ID: "RR_1"},
		{Kind: ItemReviewRequest, Author: "mhersson", Reviewer: "acme/core", ID: "RR_2"},
		{Kind: ItemReview, Author: "kari-dev", State: "APPROVED", ID: "PRR_3"},
		{Kind: ItemMerged, Author: "mhersson", ID: "ME_1"},
		{Kind: ItemClosed, Author: "mhersson", ID: "CE_1"},
	}

	got := make([]brief, 0, len(conv.Items))
	for _, it := range conv.Items {
		got = append(got, brief{it.Kind, it.Author, it.Bot, it.State, it.Commits, it.Reviewer, it.ID})
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("items = %+v\nwant %+v", got, want)
	}

	review := conv.Items[2]
	wantComments := []ReviewComment{{
		Path: "internal/ui/reviewkeys.go", Line: 212, Hunk: conversationHunk,
		Body: Text{Markdown: "Tab should hide the tree."},
	}}

	if !reflect.DeepEqual(review.Comments, wantComments) {
		t.Errorf("comments = %+v, want %+v", review.Comments, wantComments)
	}

	if review.Body.Markdown != "Please fix." {
		t.Errorf("review body = %q", review.Body.Markdown)
	}

	if fp := conv.Items[3]; fp.Ref != "fix/review-drawer" || fp.Before != "b50f694" || fp.After != "4c37f33" {
		t.Errorf("force push = %+v", fp)
	}

	if m := conv.Items[7]; m.Commit != "4c37f33" || m.Base != "main" {
		t.Errorf("merged = %+v", m)
	}

	if want := mustTime(t, "2026-10-29T10:10:00Z"); !conv.Items[1].At.Equal(want) {
		t.Errorf("push time = %v, want the last commit's %v", conv.Items[1].At, want)
	}
}

func TestConversationPushAuthor(t *testing.T) {
	t.Parallel()

	commit := func(oid, user, name string) timelineNode {
		n := timelineNode{Typename: "PullRequestCommit"}
		n.PRCommit.Commit.Oid = oid
		n.PRCommit.Commit.Author = &struct {
			Name string
			User *struct{ Login string }
		}{Name: name}

		if user != "" {
			n.PRCommit.Commit.Author.User = &struct{ Login string }{Login: user}
		}

		return n
	}

	tests := []struct {
		name  string
		nodes []timelineNode
		want  TimelineItem
	}{
		{
			name:  "last commit has no GitHub user",
			nodes: []timelineNode{commit("a", "mhersson", "M H"), commit("b", "", "Jane Doe")},
			want:  TimelineItem{Kind: ItemCommits, ID: "b", Author: "Jane Doe", Named: true, Commits: 2},
		},
		{
			name:  "last commit has a user",
			nodes: []timelineNode{commit("a", "", "Jane Doe"), commit("b", "mhersson", "M H")},
			want:  TimelineItem{Kind: ItemCommits, ID: "b", Author: "mhersson", Commits: 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			items := timelineItems(tt.nodes)
			if len(items) != 1 || !reflect.DeepEqual(items[0], tt.want) {
				t.Errorf("items = %+v, want [%+v]", items, tt.want)
			}
		})
	}
}

func TestConversationPages(t *testing.T) {
	t.Parallel()

	tmpl := string(readFixture(t, "conversation_page.json"))

	var (
		mu     sync.Mutex
		bodies []string
	)

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)

		mu.Lock()

		bodies = append(bodies, string(b))
		n := len(bodies)

		mu.Unlock()

		out := strings.NewReplacer("IC_PAGE", fmt.Sprintf("IC_PAGE_%d", n), "CURSOR", fmt.Sprintf("CURSOR_%d", n)).Replace(tmpl)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(out))
	})
	c, _ := newTestClient(t, "github.com", h)

	conv, err := c.Conversation(context.Background(), testRepo, 24)
	if err != nil {
		t.Fatal(err)
	}

	if len(bodies) != 5 {
		t.Fatalf("requests = %d, want 5", len(bodies))
	}

	for i, b := range bodies {
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		if err := json.Unmarshal([]byte(b), &req); err != nil {
			t.Fatal(err)
		}

		var want any
		if i > 0 {
			want = fmt.Sprintf("CURSOR_%d", i)
		}

		if req.Variables["cursor"] != want {
			t.Errorf("request %d cursor = %v, want %v", i+1, req.Variables["cursor"], want)
		}
	}

	if !conv.Cut {
		t.Error("Cut = false, want true")
	}

	var ids []string
	for _, it := range conv.Items {
		ids = append(ids, it.ID)
	}

	want := []string{"IC_PAGE_5", "IC_PAGE_4", "IC_PAGE_3", "IC_PAGE_2", "IC_PAGE_1"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
}

func TestConversationCursorlessPageIsCut(t *testing.T) {
	t.Parallel()

	body := strings.Replace(string(readFixture(t, "conversation_page.json")), `"startCursor": "CURSOR"`, `"startCursor": ""`, 1)
	c, _ := newTestClient(t, "github.com", &gqlRecorder{body: []byte(body)})

	conv, err := c.Conversation(context.Background(), testRepo, 24)
	if err != nil {
		t.Fatal(err)
	}

	if !conv.Cut || len(conv.Items) != 1 {
		t.Errorf("Cut = %v, items = %d, want cut with 1 item", conv.Cut, len(conv.Items))
	}
}

func TestConversationNotFound(t *testing.T) {
	t.Parallel()

	rec := &gqlRecorder{body: []byte(`{"data": {"repository": {"pullRequest": null}}}`)}
	c, _ := newTestClient(t, "github.com", rec)

	_, err := c.Conversation(context.Background(), testRepo, 24)
	if !errors.Is(err, ErrNoPR) {
		t.Fatalf("err = %v, want ErrNoPR", err)
	}
}

func TestSignedImages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, markdown, html string
		want                 map[string]string
	}{
		{
			name:     "user-attachments",
			markdown: "![shot](https://github.com/user-attachments/assets/e8b9a952-456e-4a57-b08c-012dbbee61fa)",
			html:     `<img src="https://private-user-images.githubusercontent.com/1/301-e8b9a952-456e-4a57-b08c-012dbbee61fa.png?jwt=abc&amp;x=1" alt="shot">`,
			want: map[string]string{
				"https://github.com/user-attachments/assets/e8b9a952-456e-4a57-b08c-012dbbee61fa": "https://private-user-images.githubusercontent.com/1/301-e8b9a952-456e-4a57-b08c-012dbbee61fa.png?jwt=abc&x=1",
			},
		},
		{
			name:     "old user-images",
			markdown: "![](https://user-images.githubusercontent.com/1/301-5b2c.png)",
			html:     `<img src="https://private-user-images.githubusercontent.com/1/301-5b2c.png?jwt=z">`,
			want: map[string]string{
				"https://user-images.githubusercontent.com/1/301-5b2c.png": "https://private-user-images.githubusercontent.com/1/301-5b2c.png?jwt=z",
			},
		},
		{
			name:     "img tag in markdown",
			markdown: `<img width="10" src="https://github.com/user-attachments/assets/aaaa-bbbb">`,
			html:     `<img src="https://private-user-images.githubusercontent.com/1/9-aaaa-bbbb.png?jwt=q">`,
			want: map[string]string{
				"https://github.com/user-attachments/assets/aaaa-bbbb": "https://private-user-images.githubusercontent.com/1/9-aaaa-bbbb.png?jwt=q",
			},
		},
		{
			name:     "no signed match",
			markdown: "![x](https://github.com/user-attachments/assets/1111-2222)",
			html:     `<img src="https://private-user-images.githubusercontent.com/1/301-3333-4444.png?jwt=q">`,
		},
		{
			name:     "data-src is not src",
			markdown: "![x](https://github.com/user-attachments/assets/aaaa-bbbb)",
			html:     `<img data-src="https://private-user-images.githubusercontent.com/1/9-cccc.png?jwt=no" src="https://private-user-images.githubusercontent.com/1/9-aaaa-bbbb.png?jwt=q">`,
			want: map[string]string{
				"https://github.com/user-attachments/assets/aaaa-bbbb": "https://private-user-images.githubusercontent.com/1/9-aaaa-bbbb.png?jwt=q",
			},
		},
		{
			name:     "http is not signed",
			markdown: "![x](https://github.com/user-attachments/assets/aaaa-bbbb)",
			html:     `<img src="http://private-user-images.githubusercontent.com/1/9-aaaa-bbbb.png?jwt=q">`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := signedImages(tt.markdown, tt.html); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("signedImages = %v, want %v", got, tt.want)
			}
		})
	}
}
