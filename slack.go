package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/slack-go/slack"
)

func buildSlackBlocks(tasks []Task) []slack.Block {
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
	blocks = appendSection(blocks, "🚨 今日が期限", todayTasks)
	blocks = appendSection(blocks, "⚠️ 3 日以内に期限", threeDayTasks)
	blocks = appendSection(blocks, "🗓️ 7 日以内に期限", sevenDayTasks)

	// フッター
	blocks = append(blocks, slack.NewDividerBlock())
	blocks = append(blocks, slack.NewContextBlock("", slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("生成日時: %s", now.Format(time.RFC1123)), false, false)))

	return blocks
}

// groupTasksByUrgency は、タスクを期限日に基づいて分類します。
func groupTasksByUrgency(tasks []Task) (today, threeDays, sevenDays []Task) {
	now := time.Now()
	todayBoundary := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	threeDaysBoundary := todayBoundary.AddDate(0, 0, 3)
	sevenDaysBoundary := todayBoundary.AddDate(0, 0, 7)

	for _, task := range tasks {
		dueDate := getTargetDueDate(task)
		if dueDate == nil {
			continue
		}
		dueDateTime := time.Date(dueDate.Year(), dueDate.Month(), dueDate.Day(), 0, 0, 0, 0, now.Location())

		if !dueDateTime.After(todayBoundary) {
			today = append(today, task)
		} else if !dueDateTime.After(threeDaysBoundary) { // 1 ～ 3 日以内に期限
			threeDays = append(threeDays, task)
		} else if !dueDateTime.After(sevenDaysBoundary) { // 4 ～ 7 日以内に期限
			sevenDays = append(sevenDays, task)
		}
	}
	return
}

// sortTasks は、最初に優先度 (高 > 中 > 低 > なし) でタスクをソートし、次に期限日でソートします。
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
		// nil の場合を処理 (理想的には発生しないはず)
		return dueI != nil
	})
}

// appendSection は、タスクグループのフォーマットされたセクションを Slack ブロックに追加します。
func appendSection(blocks []slack.Block, title string, tasks []Task) []slack.Block {
	if len(tasks) == 0 {
		return blocks // 空のセクションは追加しない
	}

	blocks = append(blocks, slack.NewDividerBlock())
	blocks = append(blocks, slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("*%s*", title), false, false),
		nil, nil),
	)

	for _, task := range tasks {
		taskText := fmt.Sprintf("*<%s|%s>*", task.URL, task.Title) // リンク + タイトル

		var details []string
		details = append(details, fmt.Sprintf("*期限:* %s", formatDueDate(task)))
		if task.Priority != "" {
			priorityEmoji := ""
			switch task.Priority {
			case "High":
				priorityEmoji = "🔴 "
			case "Medium":
				priorityEmoji = "🔵 "
			case "Low":
				priorityEmoji = "⚫ "
			}
			details = append(details, fmt.Sprintf("*優先度:* %s%s", priorityEmoji, task.Priority))
		}
		if task.Type != "" {
			details = append(details, fmt.Sprintf("*種類:* %s", task.Type))
		}
		if task.ScheduleStatus != "" {
			details = append(details, fmt.Sprintf("*ステータス:* %s", task.ScheduleStatus))
		}
		if task.Workload != 0 {
			details = append(details, fmt.Sprintf("*ワークロード:* %.2f", task.Workload))
		}
		// メモが存在する場合は追加。Slack ブロックの制限を超える場合は切り捨て
		if task.Memo != "" {
			maxMemoLength := 150 // メインブロックのメモの最大長
			truncatedMemo := task.Memo
			if len(truncatedMemo) > maxMemoLength {
				truncatedMemo = truncatedMemo[:maxMemoLength] + "..."
			}
			// メモ内の Markdown 文字をエスケープ
			details = append(details, fmt.Sprintf("*メモ:* %s", truncatedMemo))
		}

		// 詳細を結合。Slack の制限 (フィールドあたり約 3000 文字) を超えないようにする
		detailsText := strings.Join(details, " | ")
		if len(detailsText) > 2900 { // バッファを残す
			detailsText = detailsText[:2900] + "..."
		}

		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, taskText+"\n"+detailsText, false, false),
			nil, nil), // ここではフィールドやアクセサリは不要
		)
	}

	return blocks
}

// formatDueDate は表示用に期限日をフォーマットします。
func formatDueDate(task Task) string {
	layout := "2006-01-02" // 日付のみのレイアウト
	if task.DueEnd != nil {
		// 終了日が存在する場合、開始日も存在し、かつ異なるかどうかを確認
		if task.DueStart != nil && !time.Time(*task.DueStart).Equal(time.Time(*task.DueEnd)) {
			// 同じ日の場合は、時刻をフォーマット
			startT := time.Time(*task.DueStart)
			endT := time.Time(*task.DueEnd)
			if startT.Year() == endT.Year() && startT.Month() == endT.Month() && startT.Day() == endT.Day() {
				timeLayout := "15:04" // 時刻のみのレイアウト (利用可能な場合)
				startStr := ""
				endStr := ""
				if !startT.IsZero() && startT.Hour() != 0 || startT.Minute() != 0 { // 時刻部分に意味があるか確認
					startStr = startT.Format(timeLayout)
				}
				if !endT.IsZero() && endT.Hour() != 0 || endT.Minute() != 0 { // 時刻部分に意味があるか確認
					endStr = endT.Format(timeLayout)
				}
				if startStr != "" && endStr != "" {
					return fmt.Sprintf("%s (%s ~ %s)", endT.Format(layout), startStr, endStr)
				} else if endStr != "" { // 終了時刻のみ意味がある
					return fmt.Sprintf("%s (~%s)", endT.Format(layout), endStr)
				} else if startStr != "" { // 開始時刻のみ意味がある
					return fmt.Sprintf("%s (%s~)", endT.Format(layout), startStr)
				}
				// 時刻がゼロの場合はフォールバック
				return endT.Format(layout)

			} else {
				// 日付が異なる場合
				return fmt.Sprintf("%s ~ %s", startT.Format(layout), endT.Format(layout))
			}
		}
		// 終了日のみ存在するか、開始日と終了日が同じ場合
		return time.Time(*task.DueEnd).Format(layout)
	}

	if task.DueStart != nil {
		return time.Time(*task.DueStart).Format(layout)
	}
	return "N/A"
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
