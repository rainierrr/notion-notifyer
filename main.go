package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/jomei/notionapi"
	"github.com/slack-go/slack"
)

type Task struct {
	ID             notionapi.ObjectID
	Title          string
	DueStart       *notionapi.Date
	DueEnd         *notionapi.Date
	Priority       string // High, Medium, Low,
	Type           string
	ScheduleStatus string
	Workload       float32
	Memo           string
	URL            string
}

// 環境変数
const (
	notionTokenEnv  = "NOTION_TOKEN"
	notionDBIDEnv   = "NOTION_DB_ID" // DB ID は環境変数から取得する想定に変更
	slackTokenEnv   = "SLACK_BOT_TOKEN"
	slackChannelEnv = "SLACK_CHANNEL_ID"
)

// Notion タスクのプロパティ名
const (
	priorityProp       = "Priority"
	typeProp           = "Type"
	scheduleStatusProp = "Schedule Status"
	workloadProp       = "Workload"
	memoProp           = "Memo"
	nameProp           = "Name"
	dueProp            = "Due"
)

// 優先度の順序マッピング
var priorityOrder = map[string]int{
	"High": 1,
	"Mid":  2,
	"Low":  3,
	"":     4, // 空の優先度は最も低い
}

func main() {
	log.Println("Starting Notion Notifyer...")

	// --- 設定 ---
	notionToken := os.Getenv(notionTokenEnv)
	dbID := os.Getenv(notionDBIDEnv)
	slackToken := os.Getenv(slackTokenEnv)
	slackChannelID := os.Getenv(slackChannelEnv)

	if notionToken == "" || dbID == "" || slackToken == "" || slackChannelID == "" {
		log.Fatalf("Don't set all environment variables: %s, %s, %s, %s", notionTokenEnv, dbID, slackTokenEnv, slackChannelEnv)
	}

	notionClient := notionapi.NewClient(notionapi.Token(notionToken))
	ctx := context.Background()

	// TODO: コマンド化, 期限がちゃんと動いてないので、要修正
	sevenDaysLater := time.Date(
		time.Now().Year(),
		time.Now().Month(),
		time.Now().Day()+6,
		23, 59, 59, 59,
		time.Now().Location(),
	)

	log.Printf("Get tasks due by %s", sevenDaysLater.Format("2006-01-02"))
	// Notionからタスクを取得
	tasks, err := fetchNotionTasks(ctx, notionClient, dbID, sevenDaysLater)
	if err != nil {
		log.Fatalf("Get Notion tasks error: %v", err)
	}
	log.Printf("Get %d tasks from Notion", len(tasks))

	builtedTasks, err := buildSlackBlocks(tasks)
	if err != nil {
		log.Fatalf("Build Slack blocks error: %v", err)
	}

	slackClient := slack.New(slackToken)
	_, timestamp, err := slackClient.PostMessage(
		slackChannelID,
		slack.MsgOptionBlocks(builtedTasks...),
	)

	if err != nil {
		log.Fatalf("Slack message send error: %v", err)
	}

	log.Printf("Slack message sent to channel %s at %s", slackChannelID, timestamp)
	log.Println("Notion Notifyer finished.")
}
