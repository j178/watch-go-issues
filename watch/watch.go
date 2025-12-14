package watch

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/redis/rueidis"
	"github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"
)

var repo = struct {
	owner string
	name  string
}{"golang", "go"}

var authors = map[string]bool{
	"rsc":            true,
	"griesemer":      true,
	"ianlancetaylor": true,
	"bradfitz":       true,
	"robpike":        true,
	"mdempsky":       true,
	"randall77":      true,
	"aclements":      true,
	"cherrymui":      true,
	"mknyszek":       true,
	"thanm":          true,
	"josharian":      true,
	"adg":            true,
	"cuonglm":        true,
	"tklauser":       true,
	"prattmic":       true,
	"mvdan":          true,
	"dsnet":          true,
	"dmitshur":       true,
	"neild":          true,
}

var (
	ghToken = env("GITHUB_TOKEN")
	tgToken = env("TELEGRAM_TOKEN")
	tgChat  = env("TELEGRAM_CHAT")
	kvURL   = env("KV_URL")
)

func env(key string) string {
	for k, v := range os.Environ() {
		fmt.Printf("env %q=%q\n", k, v)
	}

	if v := os.Getenv(key); v != "" {
		return v
	}
	panic(key + " is not set")
}

func Watch() error {
	tgChatID, err := strconv.ParseInt(tgChat, 10, 64)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	src := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: ghToken},
	)
	gh := githubv4.NewClient(oauth2.NewClient(ctx, src))

	endCursor, err := getEndCursor(ctx, repo.owner, repo.name)
	if err != nil {
		return err
	}

	issues, endCursor, err := issuesAfter(ctx, gh, repo.owner, repo.name, endCursor)
	if err != nil {
		return err
	}
	for _, i := range issues {
		author := i.Author.Login
		if !authors[author] {
			continue
		}

		content := fmt.Sprintf(
			"**%s** created a new issue: [%s](%s)",
			tgbotapi.EscapeText(tgbotapi.ModeMarkdownV2, author),
			tgbotapi.EscapeText(tgbotapi.ModeMarkdownV2, i.Title),
			tgbotapi.EscapeText(tgbotapi.ModeMarkdownV2, i.URL),
		)
		err = send(ctx, tgChatID, content)
		if err != nil {
			return err
		}
	}
	log.Printf("found %d issues, end cursor %s", len(issues), endCursor)

	if len(issues) > 0 {
		err = updateEndCursor(ctx, repo.owner, repo.name, endCursor)
		if err != nil {
			return err
		}
	}

	return nil
}

type Issue struct {
	Number int
	Author struct {
		Login string
	}
	Title     string
	URL       string
	State     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Notes: 升序排列，从旧到新，每次取前 30 个。然后根据 endCursor 取下一页。

type IssueQuery struct {
	Repository struct {
		Issues struct {
			Nodes    []Issue
			PageInfo struct {
				EndCursor   string
				HasNextPage bool
			}
		} `graphql:"issues(orderBy: {field: CREATED_AT, direction: ASC}, states: [OPEN], first: 30, after: $after)"`
	} `graphql:"repository(owner: $owner, name: $name)"`
}

func issuesAfter(ctx context.Context, gh *githubv4.Client, owner, repo string, endCursor string) (
	[]Issue,
	string,
	error,
) {
	var issues []Issue
	var query IssueQuery
	vars := map[string]any{
		"owner": githubv4.String(owner),
		"name":  githubv4.String(repo),
		"after": githubv4.String(endCursor),
	}
	for page := 0; ; page++ {
		select {
		case <-ctx.Done():
			return issues, endCursor, nil
		default:
		}

		if len(issues) > 100 {
			log.Printf("too many issues: %d", len(issues))
			break
		}

		log.Printf("fetching page %d, after %s", page, endCursor)
		err := gh.Query(ctx, &query, vars)
		if err != nil {
			return nil, endCursor, err
		}
		if len(query.Repository.Issues.Nodes) != 0 {
			issues = append(issues, query.Repository.Issues.Nodes...)
			endCursor = query.Repository.Issues.PageInfo.EndCursor
		}
		if !query.Repository.Issues.PageInfo.HasNextPage {
			break
		}
		vars["after"] = githubv4.String(endCursor)
	}

	return issues, endCursor, nil
}

var redis = sync.OnceValue(
	func() rueidis.Client {
		kvURL = strings.ReplaceAll(kvURL, "redis://", "rediss://")
		opt, err := rueidis.ParseURL(kvURL)
		if err != nil {
			log.Fatalf("parse redis url: %v", err)
		}
		opt.DisableCache = true
		opt.ForceSingleClient = true

		c, err := rueidis.NewClient(opt)
		if err != nil {
			log.Fatalf("init redis: %v", err)
		}
		return c
	},
)

func getEndCursor(ctx context.Context, owner, repo string) (string, error) {
	// starts at https://github.com/golang/go/issues/64766
	const startPoint = "Y3Vyc29yOnYyOpK5MjAyMy0xMi0xNVQwNzowNzoyNSswODowMM55wC0o"

	r := redis()
	key := fmt.Sprintf("last_end_cursor:%s:%s", owner, repo)
	cmd := r.B().Get().Key(key).Build()
	t, err := r.Do(ctx, cmd).ToString()
	if rueidis.IsRedisNil(err) {
		return startPoint, nil
	}
	if err != nil {
		return "", err
	}
	return t, nil
}

func updateEndCursor(ctx context.Context, owner, repo string, endCursor string) error {
	r := redis()
	key := fmt.Sprintf("last_end_cursor:%s:%s", owner, repo)
	cmd := r.B().Set().Key(key).Value(endCursor).Build()
	err := r.Do(ctx, cmd).Error()
	if err != nil {
		return err
	}
	return nil
}

var bot = sync.OnceValue(
	func() *tgbotapi.BotAPI {
		bot, err := tgbotapi.NewBotAPI(tgToken)
		if err != nil {
			log.Fatalf("init telegram bot: %v", err)
		}
		return bot
	},
)

func send(ctx context.Context, chatID int64, content string) error {
	msg := tgbotapi.NewMessage(chatID, content)
	msg.ParseMode = "MarkdownV2"

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		_, err := bot().Send(msg)
		return err
	}
}
