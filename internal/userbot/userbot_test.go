package userbot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/sirupsen/logrus"

	"bot/internal/config"
)

type fakeMongoChecker struct {
	err error
}

func (f fakeMongoChecker) Ping(context.Context) error {
	return f.err
}

type fakeStatsProvider struct {
	users    int64
	groups   int64
	userErr  error
	groupErr error
}

func (f fakeStatsProvider) CountUsers(context.Context) (int64, error) {
	return f.users, f.userErr
}

func (f fakeStatsProvider) CountGroups(context.Context) (int64, error) {
	return f.groups, f.groupErr
}

func TestOwnerCommandRecognition(t *testing.T) {
	tests := []struct {
		name string
		meta messageMeta
		want bool
	}{
		{
			name: "owner outgoing dot command",
			meta: messageMeta{userID: 42, out: true, text: ".ping"},
			want: true,
		},
		{
			name: "incoming command ignored",
			meta: messageMeta{userID: 42, out: false, text: ".ping"},
		},
		{
			name: "non owner ignored",
			meta: messageMeta{userID: 41, out: true, text: ".ping"},
		},
		{
			name: "plain message ignored",
			meta: messageMeta{userID: 42, out: true, text: "ping"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isOwnerCommand(tt.meta, 42); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestCommandName(t *testing.T) {
	if got := commandName(".PING now"); got != "ping" {
		t.Fatalf("expected ping, got %q", got)
	}
	if got := commandName("ping"); got != "" {
		t.Fatalf("expected empty command, got %q", got)
	}
}

func TestCommandResponses(t *testing.T) {
	client := &Client{
		cfg: config.Config{
			AppEnv: EnvForTest,
		},
		logger:        logrus.NewEntry(logrus.New()),
		mongoChecker:  fakeMongoChecker{},
		processStart:  time.Now().Add(-2 * time.Minute),
		statsProvider: fakeStatsProvider{users: 3, groups: 4},
	}

	ping, handled := client.commandResponse(context.Background(), "ping")
	if !handled {
		t.Fatalf("expected ping to be handled")
	}
	if !strings.Contains(ping, "pong") || !strings.Contains(ping, "mongo: ok") {
		t.Fatalf("unexpected ping response: %q", ping)
	}

	status, handled := client.commandResponse(context.Background(), "status")
	if !handled {
		t.Fatalf("expected status to be handled")
	}
	for _, part := range []string{"userbot_status: running", "registered_users: 3", "known_chats: 4"} {
		if !strings.Contains(status, part) {
			t.Fatalf("expected status to contain %q, got %q", part, status)
		}
	}
}

func TestCommandResponsesSurfaceDependencyErrors(t *testing.T) {
	client := &Client{
		cfg:          config.Config{AppEnv: EnvForTest},
		logger:       logrus.NewEntry(logrus.New()),
		mongoChecker: fakeMongoChecker{err: errors.New("mongo down")},
		processStart: time.Now(),
		statsProvider: fakeStatsProvider{
			userErr:  errors.New("users down"),
			groupErr: errors.New("groups down"),
		},
	}

	status := client.statusResponse(context.Background())
	for _, part := range []string{"mongo: error", "registered_users: error", "known_chats: error"} {
		if !strings.Contains(status, part) {
			t.Fatalf("expected status to contain %q, got %q", part, status)
		}
	}
}

func TestInputPeerFromPeerAllowsBasicGroupWithoutEntity(t *testing.T) {
	inputPeer, err := inputPeerFromPeer(tg.Entities{}, &tg.PeerChat{ChatID: 4750561458})
	if err != nil {
		t.Fatalf("expected basic group input peer, got error: %v", err)
	}

	chat, ok := inputPeer.(*tg.InputPeerChat)
	if !ok {
		t.Fatalf("expected InputPeerChat, got %T", inputPeer)
	}
	if chat.ChatID != 4750561458 {
		t.Fatalf("expected chat id 4750561458, got %d", chat.ChatID)
	}
}

const EnvForTest = "test"
