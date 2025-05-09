package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

const (
	MAX_MESSAGE_LENGTH = 3000 // Slack メッセージの最大長
	MAX_MEMO_LENGTH    = 1000 // メモの最大長
)

func buildSlackBlocks(tasks []Task) ([]slack.Block, error) {
	if len(tasks) == 0 {
		return nil, nil
	}
	now := time.Now()
	// タスクを緊急度でグループ化
	todayTasks, threeDayTasks, sevenDayTasks := groupTasksByUrgency(tasks)

	// 各グループ内でタスクをソート
	sortTasks(todayTasks)
	sortTasks(threeDayTasks)
	sortTasks(sevenDayTasks)

	var blocks []slack.Block

	// ヘッダー
	blocks = append(blocks, slack.NewHeaderBlock(slack.NewTextBlockObject(slack.PlainTextType, "🔔 Notion タスクリマインダー", true, false)))

	// 各グループにタスクがある場合は、セクションを追加
	blocks, err := appendSection(blocks, "🚨 今日が期限", todayTasks)
	if err != nil {
		return blocks, err
	}
	blocks, err = appendSection(blocks, "⚠️ 3 日以内に期限", threeDayTasks)
	if err != nil {
		return blocks, err
	}
	blocks, err = appendSection(blocks, "🗓️ 7 日以内に期限", sevenDayTasks)
	if err != nil {
		return blocks, err
	}

	// フッター
	blocks = append(blocks, slack.NewDividerBlock())
	blocks = append(blocks, slack.NewContextBlock("", slack.NewTextBlockObject(slack.PlainTextType, fmt.Sprintf("CreatedAt: %s", now.Format(time.RFC1123)), false, false)))

	return blocks, nil
}

// groupTasksByUrgency は、タスクを期限日に基づいて分類します。
func groupTasksByUrgency(tasks []Task) (today, threeDays, sevenDays []Task) {
	now := time.Now()

	todayBoundary := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, now.Location())
	threeDaysBoundary := todayBoundary.AddDate(0, 0, 3)
	sevenDaysBoundary := todayBoundary.AddDate(0, 0, 7)

	for _, task := range tasks {
		dueDate := getTargetDueDate(task)
		if dueDate == nil {
			continue
		}

		if !dueDate.After(todayBoundary) {
			today = append(today, task)
		} else if !dueDate.After(threeDaysBoundary) { // 1 ～ 3 日以内に期限
			threeDays = append(threeDays, task)
		} else if !dueDate.After(sevenDaysBoundary) { // 4 ～ 7 日以内に期限
			sevenDays = append(sevenDays, task)
		}
	}
	return
}

// タスクを優先度と期限日でソート
func sortTasks(tasks []Task) {
	sort.SliceStable(tasks, func(i, j int) bool {
		priI := priorityOrder[tasks[i].Priority]
		priJ := priorityOrder[tasks[j].Priority]
		if priI != priJ {
			return priI < priJ // 数値が小さいほど優先度が高い
		}
		// 優先度が同じ場合は、期限日でソート (早い順)
		dueI := getTargetDueDate(tasks[i])
		dueJ := getTargetDueDate(tasks[j])
		if dueI != nil && dueJ != nil {
			return dueI.Before(*dueJ)
		}
		return false // どちらかが nil の場合は、順序を変更しない
	})
}

func appendSection(blocks []slack.Block, title string, tasks []Task) ([]slack.Block, error) {
	if len(tasks) == 0 {
		return blocks, nil
	}

	blocks = append(blocks, slack.NewDividerBlock())
	blocks = append(blocks, slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("*%s*", title), false, false),
		nil, nil),
	)

	for _, task := range tasks {
		strTaskTitle := fmt.Sprintf("*<%s|%s>*", task.URL, task.Title) // リンク + タイトル

		var details []string
		strTime, err := formatDueDate(task)
		if err != nil {
			return blocks, fmt.Errorf("failed to format due date for task %s: %w", task.Title, err)
		}
		details = append(details, fmt.Sprintf("*期限日:* %s", strTime))
		if task.Priority != "" {
			details = append(details, fmt.Sprintf("*優先度:* %s", task.Priority))
		}
		if task.Type != "" {
			details = append(details, fmt.Sprintf("*種類:* %s", task.Type))
		}
		if task.ScheduleStatus != "" {
			details = append(details, fmt.Sprintf("*スケジュール:* %s", task.ScheduleStatus))
		}
		if task.Workload != 0 {
			details = append(details, fmt.Sprintf("*ワークロード:* %.2f", task.Workload))
		}

		if task.Memo != "" {
			truncatedMemo := task.Memo
			// メモが長すぎる場合は切り捨て
			if len(truncatedMemo) > MAX_MEMO_LENGTH {
				truncatedMemo = truncatedMemo[:MAX_MEMO_LENGTH] + "..."
			}
			details = append(details, fmt.Sprintf("*メモ:* %s", truncatedMemo))
		}

		// 文字数制限を超える場合は切り捨て
		detailsText := strings.Join(details, " | ")
		if len(detailsText) > MAX_MESSAGE_LENGTH {
			detailsText = detailsText[:MAX_MESSAGE_LENGTH] + "..."
		}

		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, strTaskTitle+"\n"+detailsText, false, false),
			nil, nil),
		)
	}

	return blocks, nil
}

// formatDueDate は表示用に期限日をフォーマットします。
func formatDueDate(task Task) (string, error) {
	startTime := task.DueStart
	endTime := task.DueEnd

	if startTime == nil && endTime == nil {
		return "", errors.New("startTime and endTime are both nil")
	}

	if startTime != nil && endTime != nil {
		startTimeStr := timeFormat(time.Time(*startTime))
		endTimeStr := timeFormat(time.Time(*endTime))
		return fmt.Sprintf("%s ~ %s", startTimeStr, endTimeStr), nil
	}

	return timeFormat(time.Time(*startTime)), nil
}

// タスクの目標期限日を取得 (endDate優先)
func getTargetDueDate(task Task) *time.Time {
	if task.DueEnd != nil {
		t := time.Time(*task.DueEnd)
		return &t
	}
	if task.DueStart != nil {
		t := time.Time(*task.DueStart)
		return &t
	}
	return nil
}

func timeFormat(t time.Time) string {
	month := int(t.Month())
	day := t.Day()
	hour := t.Hour()
	minute := t.Minute()
	if hour != 0 {
		return fmt.Sprintf("%02d/%02d %02d:%02d", month, day, hour, minute)
	}
	return fmt.Sprintf("%02d/%02d", month, day)
}
