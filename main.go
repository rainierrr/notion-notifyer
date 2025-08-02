package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/jomei/notionapi"
	"github.com/slack-go/slack"
	"github.com/spf13/cobra"
)

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

var rootCmd = &cobra.Command{
	Use:   "notion-notifyer",
	Short: "Notion Notifyer sends Slack notifications for Notion tasks.",
	Run: func(cmd *cobra.Command, args []string) {
		log.Println("Starting Notion Notifyer...")

		// GitHub Actions Run Numberを取得
		runNumber := os.Getenv("GITHUB_RUN_NUMBER")
		if runNumber != "" {
			log.Printf("GitHub Actions Run Number: %s", runNumber)
		}

		daysLater, _ := cmd.Flags().GetInt("daysLater")
		if daysLater > 3 {
			log.Printf("Warning: daysLater is limited to 3 days maximum. Using 3 instead of %d", daysLater)
			daysLater = 3
		}

		notionToken := os.Getenv(notionTokenEnv)
		dbID := os.Getenv(notionDBIDEnv)
		slackToken := os.Getenv(slackTokenEnv)
		slackChannelID := os.Getenv(slackChannelEnv)

		if notionToken == "" || dbID == "" || slackToken == "" || slackChannelID == "" {
			log.Fatalf("Don't set all environment variables: %s, %s, %s, %s", notionTokenEnv, notionDBIDEnv, slackTokenEnv, slackChannelEnv)
		}

		notionClient := notionapi.NewClient(notionapi.Token(notionToken))
		ctx := context.Background()

		targetDate := time.Date(
			time.Now().Year(),
			time.Now().Month(),
			time.Now().Day()+daysLater,
			23, 59, 59, 59,
			time.Now().Location(),
		)

		log.Printf("Get tasks due by %s", targetDate.Format("2006-01-02"))

		// Notionからタスクを取得
		tasks, err := fetchNotionTasks(ctx, notionClient, dbID, targetDate)
		if err != nil {
			log.Fatalf("Get Notion tasks error: %v", err)
		}
		log.Printf("Get %d tasks from Notion", len(tasks))

		if len(tasks) == 0 {
			log.Println("No tasks found.")
			return
		}

		builtedTasks, err := buildSlackBlocks(tasks, runNumber)
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
	},
}

func init() {
	rootCmd.PersistentFlags().IntP("daysLater", "d", 0, "Number of days later to check for due tasks (e.g., 0 for today, 3 for 3 days later)")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatalf("Error executing command: %v", err)
		os.Exit(1)
	}
}
