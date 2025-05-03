package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jomei/notionapi"
	"github.com/slack-go/slack"
)

// Task は Notion から取得したタスクを表します
type Task struct {
	ID             notionapi.ObjectID // PageID から ObjectID (string) に変更
	Title          string
	DueStart       *notionapi.Date
	DueEnd         *notionapi.Date
	Priority       string // High, Medium, Low, ""
	Type           string
	ScheduleStatus string
	Workload       string
	Memo           string
	URL            string
	NotifyDays     int // カスタム通知日数 (1, 3, 7, またはデフォルト 3)
}

// デフォルト値と設定キーの定数
const (
	defaultNotifyDays  = 3
	notionTokenEnv     = "NOTION_TOKEN"
	notionDBIDEnv      = "NOTION_DB_ID" // DB ID は環境変数から取得する想定に変更
	slackTokenEnv      = "SLACK_BOT_TOKEN"
	slackChannelEnv    = "SLACK_CHANNEL_ID"
	notifyDaysProp     = "通知日数" // カスタム通知日数プロパティ名
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
	"High":   1,
	"Medium": 2,
	"Low":    3,
	"":       4, // 空の優先度は最低として扱う
}

func main() {
	log.Println("Notion Notifyer を開始します...")

	// --- 設定 ---
	notionToken := os.Getenv(notionTokenEnv)
	dbID := os.Getenv(notionDBIDEnv)
	slackToken := os.Getenv(slackTokenEnv)
	slackChannelID := os.Getenv(slackChannelEnv)

	if notionToken == "" || dbID == "" || slackToken == "" || slackChannelID == "" {
		log.Fatalf("必要な環境変数がありません: %s, %s, %s, %s",
			notionTokenEnv, notionDBIDEnv, slackTokenEnv, slackChannelEnv)
	}

	// --- Notion クライアント ---
	notionClient := notionapi.NewClient(notionapi.Token(notionToken))
	ctx := context.Background()

	// --- Notion からタスクを取得 ---
	tasks, err := fetchNotionTasks(ctx, notionClient, dbID)
	if err != nil {
		log.Fatalf("Notion タスクの取得エラー: %v", err)
	}
	log.Printf("最初に %d 件のタスクを取得しました。", len(tasks))

	// --- タスクのフィルタリング ---
	now := time.Now()
	tasksToNotify := filterTasks(tasks, now)
	log.Printf("通知対象の可能性のあるタスク %d 件に絞り込みました。", len(tasksToNotify))

	if len(tasksToNotify) == 0 {
		log.Println("本日は通知するタスクはありません。")
		return
	}

	// --- Slack メッセージのフォーマット ---
	message := formatSlackMessage(tasksToNotify, now)
	if message == "" {
		log.Println("フォーマットされたメッセージが空です。送信するものはありません。")
		return
	}

	// --- Slack へ送信 ---
	slackClient := slack.New(slackToken)
	_, timestamp, err := slackClient.PostMessage(
		slackChannelID,
		slack.MsgOptionText(message, false), // フォールバックテキスト
		slack.MsgOptionBlocks(buildSlackBlocks(tasksToNotify, now)...),
		// slack.MsgOptionAsUser(true), // ボットトークンと競合する可能性があるため削除
	)
	if err != nil {
		log.Fatalf("Slack メッセージの送信エラー: %v", err)
	}

	log.Printf("Slack チャンネル %s に通知を送信しました: %s", slackChannelID, timestamp)
	log.Println("Notion Notifyer が完了しました。")
}

// fetchNotionTasks は指定された Notion データベースからタスクを取得します。
func fetchNotionTasks(ctx context.Context, client *notionapi.Client, dbID string) ([]Task, error) {
	var allTasks []Task
	var cursor notionapi.Cursor
	sevenDaysLater := time.Now().AddDate(0, 0, 7) // 次の7日以内に期限が来るタスクを取得

	for {
		request := &notionapi.DatabaseQueryRequest{
			Filter: &notionapi.AndCompoundFilter{
				// 期限日が今から7日以内
				&notionapi.PropertyFilter{
					Property: dueProp,
					Date: &notionapi.DateFilterCondition{
						OnOrBefore: (*notionapi.Date)(&sevenDaysLater),
					},
				},
				// 期限日が空でない
				&notionapi.PropertyFilter{
					Property: dueProp,
					Date: &notionapi.DateFilterCondition{
						IsNotEmpty: true,
					},
				},
				// スケジュールステータスがアクティブ/保留中のいずれか
				createStatusFilter(),
			},
			Sorts: []notionapi.SortObject{
				{Property: dueProp, Direction: notionapi.SortOrderASC}, // 期限日で基本的な昇順ソート
			},
			PageSize:    100, // 最大ページサイズ
			StartCursor: cursor,
		}

		resp, err := client.Database.Query(ctx, notionapi.DatabaseID(dbID), request)
		if err != nil {
			return nil, fmt.Errorf("データベースクエリ失敗: %w", err)
		}

		for _, page := range resp.Results {
			task := parseNotionPage(page)
			if task != nil {
				allTasks = append(allTasks, *task)
			}
		}

		if !resp.HasMore {
			break
		}
		cursor = resp.NextCursor
	}

	return allTasks, nil
}

// createStatusFilter は関連するスケジュールステータスの OR フィルターを生成します。
func createStatusFilter() notionapi.OrCompoundFilter {
	// タスクがまだ完了またはアーカイブされていないことを示すステータス
	relevantStatuses := []string{"CannotDo", "Next", "Want", "ToDo", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday", "Doing"} // "Doing" を追加
	var filters []notionapi.Filter
	for _, status := range relevantStatuses {
		filters = append(filters, &notionapi.PropertyFilter{
			Property: scheduleStatusProp,
			Status: &notionapi.StatusFilterCondition{
				Equals: status,
			},
		})
	}
	return notionapi.OrCompoundFilter(filters)
}

// parseNotionPage は Notion ページオブジェクトを Task 構造体に変換します。
func parseNotionPage(page notionapi.Page) *Task {
	task := Task{
		ID:         page.ID,
		URL:        page.URL,
		NotifyDays: defaultNotifyDays, // デフォルトの通知日数
	}

	// プロパティを安全に反復処理
	for propName, propValue := range page.Properties {
		switch propName {
		case nameProp:
			if p, ok := propValue.(*notionapi.TitleProperty); ok && len(p.Title) > 0 {
				task.Title = p.Title[0].Text.Content
			}
		case dueProp:
			if p, ok := propValue.(*notionapi.DateProperty); ok && p.Date != nil {
				task.DueStart = p.Date.Start
				task.DueEnd = p.Date.End // 時間や終了日がない日付の場合は nil になる可能性あり
			}
		case priorityProp:
			if p, ok := propValue.(*notionapi.SelectProperty); ok && p.Select.Name != "" { // nil の代わりに Name をチェック
				task.Priority = p.Select.Name
			}
		case typeProp:
			if p, ok := propValue.(*notionapi.SelectProperty); ok && p.Select.Name != "" { // nil の代わりに Name をチェック
				task.Type = p.Select.Name
			}
		case scheduleStatusProp:
			if p, ok := propValue.(*notionapi.StatusProperty); ok && p.Status.Name != "" { // nil の代わりに Name をチェック
				task.ScheduleStatus = p.Status.Name
			}
		case workloadProp:
			if p, ok := propValue.(*notionapi.SelectProperty); ok && p.Select.Name != "" { // nil の代わりに Name をチェック
				task.Workload = p.Select.Name
			}
		case memoProp:
			// メモは空か、複数のリッチテキスト部分を持つ可能性がある
			if p, ok := propValue.(*notionapi.RichTextProperty); ok && len(p.RichText) > 0 {
				var memoBuilder strings.Builder
				for i, rt := range p.RichText {
					if i > 0 {
						memoBuilder.WriteString("\n") // 必要に応じてパーツ間に改行を追加
					}
					memoBuilder.WriteString(rt.Text.Content)
				}
				task.Memo = memoBuilder.String()
			}
		case notifyDaysProp:
			if p, ok := propValue.(*notionapi.SelectProperty); ok && p.Select.Name != "" { // nil の代わりに Name をチェック
				if days, err := strconv.Atoi(p.Select.Name); err == nil && (days == 1 || days == 3 || days == 7) {
					task.NotifyDays = days
				} else {
					log.Printf("警告: '%s' の '%s' に無効な値 '%s' が指定されています。デフォルトの %d 日を使用します。",
						task.Title, notifyDaysProp, p.Select.Name, defaultNotifyDays)
				}
			}
		}
	}

	// 必須プロパティの検証: タイトルと期限日は必須
	if task.Title == "" || (task.DueStart == nil && task.DueEnd == nil) {
		log.Printf("警告: タイトルまたは期限日が欠落しているため、ページ ID %s をスキップします。", page.ID)
		return nil
	}

	return &task
}

// filterTasks は、通知ルールと現在の時刻に基づいて、取得したタスクをフィルタリングします。
func filterTasks(tasks []Task, now time.Time) []Task {
	var filtered []Task
	currentHour := now.Hour()
	isUrgentNotificationTime := (currentHour == 13 || currentHour == 20) // 午後 1 時または午後 8 時

	for _, task := range tasks {
		if shouldNotify(task, now) {
			// 午後 1 時または午後 8 時の場合は、今日が期限のタスクのみを含めます
			if isUrgentNotificationTime {
				if isDueToday(task, now) {
					filtered = append(filtered, task)
				}
			} else {
				// それ以外の場合 (例: 午前 9 時)、通知基準を満たすすべてのタスクを含めます
				filtered = append(filtered, task)
			}
		}
	}
	return filtered
}

// shouldNotify は、タスクの期限とカスタム設定に基づいて、タスク通知を送信する必要があるかどうかを判断します。
func shouldNotify(task Task, now time.Time) bool {
	targetDate := getTargetDueDate(task)
	if targetDate == nil {
		return false // 念のため
	}

	// 残り日数を計算 (日付部分のみを考慮)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dueDate := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, now.Location())

	// 期限日が過去の場合は通知しない (今日の場合は除く)
	if dueDate.Before(today) {
		return false
	}

	daysRemaining := int(dueDate.Sub(today).Hours() / 24)

	// 残り日数が設定された通知日数以下の場合に通知
	return daysRemaining <= task.NotifyDays
}

// isDueToday は、タスクの目標期限日が今日であるかどうかを確認します。
func isDueToday(task Task, now time.Time) bool {
	targetDate := getTargetDueDate(task)
	if targetDate == nil {
		return false
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dueDate := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, now.Location())
	return dueDate.Equal(today)
}

// getTargetDueDate は、有効な期限日 (終了日優先、次に開始日) を返します。
func getTargetDueDate(task Task) *time.Time {
	if task.DueEnd != nil {
		t := time.Time(*task.DueEnd)
		return &t
	}
	if task.DueStart != nil {
		t := time.Time(*task.DueStart)
		return &t
	}
	return nil // 期限日は必須なので、ここには来ないはず
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
	// 開始日のみ存在する場合
	if task.DueStart != nil {
		return time.Time(*task.DueStart).Format(layout)
	}
	return "N/A" // ここには来ないはず
}

// --- Slack のフォーマットと送信 ---

// buildSlackBlocks は Slack メッセージのブロックを作成します。
func buildSlackBlocks(tasks []Task, now time.Time) []slack.Block {
	// タスクを緊急度でグループ化
	todayTasks, threeDayTasks, sevenDayTasks := groupTasksByUrgency(tasks, now)

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
func groupTasksByUrgency(tasks []Task, now time.Time) (today, threeDays, sevenDays []Task) {
	todayBoundary := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	threeDaysBoundary := todayBoundary.AddDate(0, 0, 3)
	sevenDaysBoundary := todayBoundary.AddDate(0, 0, 7) // 最初の取得は 7 日間ですが、明示的にグループ化

	for _, task := range tasks {
		dueDate := getTargetDueDate(task)
		if dueDate == nil {
			continue
		}
		dueDateTime := time.Date(dueDate.Year(), dueDate.Month(), dueDate.Day(), 0, 0, 0, 0, now.Location())

		if !dueDateTime.After(todayBoundary) { // 今日が期限または期限切れ (フィルターにより期限切れは発生しないはず)
			today = append(today, task)
		} else if !dueDateTime.After(threeDaysBoundary) { // 1 ～ 3 日以内に期限
			threeDays = append(threeDays, task)
		} else if !dueDateTime.After(sevenDaysBoundary) { // 4 ～ 7 日以内に期限
			sevenDays = append(sevenDays, task)
		}
		// 7 日より後のタスクはすでに除外されています
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
		if task.Workload != "" {
			details = append(details, fmt.Sprintf("*ワークロード:* %s", task.Workload))
		}
		// メモが存在する場合は追加。Slack ブロックの制限を超える場合は切り捨て
		if task.Memo != "" {
			maxMemoLength := 150 // メインブロックのメモの最大長
			truncatedMemo := task.Memo
			if len(truncatedMemo) > maxMemoLength {
				truncatedMemo = truncatedMemo[:maxMemoLength] + "..."
			}
			// メモ内の Markdown 文字をエスケープ
			escapedMemo := strings.ReplaceAll(truncatedMemo, "*", "\\*")
			escapedMemo = strings.ReplaceAll(truncatedMemo, "_", "\\_")
			escapedMemo = strings.ReplaceAll(truncatedMemo, "~", "\\~")
			escapedMemo = strings.ReplaceAll(truncatedMemo, "`", "\\`")

			details = append(details, fmt.Sprintf("*メモ:* %s", escapedMemo))
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

// formatSlackMessage は、シンプルなテキストのフォールバックメッセージを作成します (Block Kit ではあまり使用されません)。
func formatSlackMessage(tasks []Task, now time.Time) string {
	// これは主に、ブロックのレンダリングに失敗した場合のフォールバックです
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("Notion タスクリマインダー (%s)\n", now.Format("2006-01-02")))

	today, threeDays, sevenDays := groupTasksByUrgency(tasks, now)
	sortTasks(today)
	sortTasks(threeDays)
	sortTasks(sevenDays)

	appendTasksToString(&builder, "今日が期限", today)
	appendTasksToString(&builder, "3 日以内に期限", threeDays)
	appendTasksToString(&builder, "7 日以内に期限", sevenDays)

	return builder.String()
}

// appendTasksToString は、フォールバックテキストメッセージのヘルパーです。
func appendTasksToString(builder *strings.Builder, title string, tasks []Task) {
	if len(tasks) > 0 {
		builder.WriteString(fmt.Sprintf("\n*%s*\n", title))
		for _, task := range tasks {
			builder.WriteString(fmt.Sprintf("- *%s* (期限: %s", task.Title, formatDueDate(task)))
			if task.Priority != "" {
				builder.WriteString(fmt.Sprintf(", 優先度: %s", task.Priority))
			}
			builder.WriteString(fmt.Sprintf(") <%s>\n", task.URL))
		}
	}
}
