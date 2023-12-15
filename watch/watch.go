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
	"github.com/google/go-github/v57/github"
	"github.com/redis/rueidis"
)

var authors = map[string]bool{
	"rsc":  true,
	"j178": true,
}

var (
	ghToken      = env("GITHUB_TOKEN")
	tgToken      = env("TELEGRAM_TOKEN")
	tgChat       = env("TELEGRAM_CHAT")
	repoFullName = env("REPO")
	kvURL        = env("KV_URL")
)

func env(key string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	panic(key + " is not set")
}

func Watch() error {
	gh := github.NewClient(nil)
	gh.WithAuthToken(ghToken)

	tgChat, err := strconv.ParseInt(tgChat, 10, 64)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	parts := strings.Split(repoFullName, "/")
	owner, repo := parts[0], parts[1]
	lastCreatedAt, err := getLastCreatedAt(ctx, owner, repo)
	if err != nil {
		return err
	}
	if lastCreatedAt.IsZero() {
		lastCreatedAt = time.Now()
	}

	issues, err := issuesAfter(ctx, gh, owner, repo, lastCreatedAt)
	if err != nil {
		return err
	}
	for _, i := range issues {
		author := i.GetUser().GetLogin()
		if !authors[author] {
			continue
		}

		content := fmt.Sprintf(
			"[%s](%s) created a new issue: [%s](%s)",
			EscapeMarkdown(author),
			EscapeMarkdown(i.GetUser().GetHTMLURL()),
			EscapeMarkdown(i.GetTitle()),
			EscapeMarkdown(i.GetHTMLURL()),
		)
		err = send(ctx, tgChat, content)
		if err != nil {
			return err
		}
	}

	err = updateLastCreatedAt(ctx, owner, repo, time.Now())
	if err != nil {
		return err
	}
	return nil
}

func issuesAfter(ctx context.Context, gh *github.Client, owner, repo string, lastCreatedAt time.Time) ([]*github.Issue, error) {
	var issues []*github.Issue
	var page int
	for {
		select {
		case <-ctx.Done():
			return issues, nil
		default:
		}
		log.Printf("fetching page %d, since %s", page, lastCreatedAt)
		es, resp, err := gh.Issues.ListByRepo(ctx, owner, repo, &github.IssueListByRepoOptions{
			Since: lastCreatedAt,
			State: "open",
			ListOptions: github.ListOptions{
				Page: page,
			},
		})
		if err != nil {
			return nil, err
		}
		issues = append(issues, es...)
		if resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}
	return issues, nil
}

var redis = sync.OnceValue(func() rueidis.Client {
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
})

func getLastCreatedAt(ctx context.Context, owner, repo string) (time.Time, error) {
	r := redis()
	key := fmt.Sprintf("last_created_at:%s:%s", owner, repo)
	cmd := r.B().Get().Key(key).Build()
	t, err := r.Do(ctx, cmd).AsInt64()
	if rueidis.IsRedisNil(err) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(t, 0), nil
}

func updateLastCreatedAt(ctx context.Context, owner, repo string, createdAt time.Time) error {
	r := redis()
	key := fmt.Sprintf("last_created_at:%s:%s", owner, repo)
	v := strconv.FormatInt(createdAt.Unix(), 10)
	cmd := r.B().Set().Key(key).Value(v).Build()
	err := r.Do(ctx, cmd).Error()
	if err != nil {
		return err
	}
	return nil
}

var bot = sync.OnceValue(func() *tgbotapi.BotAPI {
	bot, err := tgbotapi.NewBotAPI(tgToken)
	if err != nil {
		log.Fatalf("init telegram bot: %v", err)
	}
	return bot
})

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

var markdownReplacer = strings.NewReplacer(
	"_", "\\_", "*", "\\*", "[", "\\[", "]", "\\]", "(",
	"\\(", ")", "\\)", "~", "\\~", "`", "\\`", ">", "\\>",
	"#", "\\#", "+", "\\+", "-", "\\-", "=", "\\=", "|",
	"\\|", "{", "\\{", "}", "\\}", ".", "\\.", "!", "\\!",
	"\\", "\\\\",
)

func EscapeMarkdown(text string) string {
	return markdownReplacer.Replace(text)
}
