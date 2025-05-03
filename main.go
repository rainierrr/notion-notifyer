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

// Task represents a task fetched from Notion
type Task struct {
	ID             notionapi.ObjectID // Changed from PageID to ObjectID (string)
	Title          string
	DueStart       *notionapi.Date
	DueEnd         *notionapi.Date
	Priority       string // High, Medium, Low, ""
	Type           string
	ScheduleStatus string
	Workload       string
	Memo           string
	URL            string
	NotifyDays     int // Custom notification days (1, 3, 7, or default 3)
}

// Constants for default values and configuration keys
const (
	defaultNotifyDays  = 3
	notionTokenEnv     = "NOTION_TOKEN"
	notionDBIDEnv      = "NOTION_DB_ID" // Expecting DB ID from env var now
	slackTokenEnv      = "SLACK_BOT_TOKEN"
	slackChannelEnv    = "SLACK_CHANNEL_ID"
	notifyDaysProp     = "通知日数" // Custom notification days property name
	priorityProp       = "Priority"
	typeProp           = "Type"
	scheduleStatusProp = "Schedule Status"
	workloadProp       = "Workload"
	memoProp           = "Memo"
	nameProp           = "Name"
	dueProp            = "Due"
)

// Priority order mapping
var priorityOrder = map[string]int{
	"High":   1,
	"Medium": 2,
	"Low":    3,
	"":       4, // Treat empty priority as lowest
}

func main() {
	log.Println("Starting Notion Notifyer...")

	// --- Configuration ---
	notionToken := os.Getenv(notionTokenEnv)
	dbID := os.Getenv(notionDBIDEnv)
	slackToken := os.Getenv(slackTokenEnv)
	slackChannelID := os.Getenv(slackChannelEnv)

	if notionToken == "" || dbID == "" || slackToken == "" || slackChannelID == "" {
		log.Fatalf("Missing required environment variables: %s, %s, %s, %s",
			notionTokenEnv, notionDBIDEnv, slackTokenEnv, slackChannelEnv)
	}

	// --- Notion Client ---
	notionClient := notionapi.NewClient(notionapi.Token(notionToken))
	ctx := context.Background()

	// --- Fetch Tasks from Notion ---
	tasks, err := fetchNotionTasks(ctx, notionClient, dbID)
	if err != nil {
		log.Fatalf("Error fetching Notion tasks: %v", err)
	}
	log.Printf("Fetched %d tasks initially.", len(tasks))

	// --- Filter Tasks ---
	now := time.Now()
	tasksToNotify := filterTasks(tasks, now)
	log.Printf("Filtered down to %d tasks to potentially notify.", len(tasksToNotify))

	if len(tasksToNotify) == 0 {
		log.Println("No tasks to notify today.")
		return
	}

	// --- Format Slack Message ---
	message := formatSlackMessage(tasksToNotify, now)
	if message == "" {
		log.Println("Formatted message is empty, nothing to send.")
		return
	}

	// --- Send to Slack ---
	slackClient := slack.New(slackToken)
	_, timestamp, err := slackClient.PostMessage(
		slackChannelID,
		slack.MsgOptionText(message, false), // Fallback text
		slack.MsgOptionBlocks(buildSlackBlocks(tasksToNotify, now)...),
		// slack.MsgOptionAsUser(true), // Removed this option as it might conflict with bot tokens
	)
	if err != nil {
		log.Fatalf("Error sending Slack message: %v", err)
	}

	log.Printf("Successfully sent notification to Slack channel %s at %s", slackChannelID, timestamp)
	log.Println("Notion Notifyer finished.")
}

// fetchNotionTasks fetches tasks from the specified Notion database.
func fetchNotionTasks(ctx context.Context, client *notionapi.Client, dbID string) ([]Task, error) {
	var allTasks []Task
	var cursor notionapi.Cursor
	sevenDaysLater := time.Now().AddDate(0, 0, 7) // Fetch tasks due within the next 7 days

	for {
		request := &notionapi.DatabaseQueryRequest{
			Filter: &notionapi.AndCompoundFilter{
				// Due date is on or before 7 days from now
				&notionapi.PropertyFilter{
					Property: dueProp,
					Date: &notionapi.DateFilterCondition{
						OnOrBefore: (*notionapi.Date)(&sevenDaysLater),
					},
				},
				// Due date is not empty
				&notionapi.PropertyFilter{
					Property: dueProp,
					Date: &notionapi.DateFilterCondition{
						IsNotEmpty: true,
					},
				},
				// Schedule Status is one of the active/pending statuses
				createStatusFilter(),
			},
			Sorts: []notionapi.SortObject{
				{Property: dueProp, Direction: notionapi.SortOrderASC}, // Basic sort by due date
			},
			PageSize:    100, // Max page size
			StartCursor: cursor,
		}

		resp, err := client.Database.Query(ctx, notionapi.DatabaseID(dbID), request)
		if err != nil {
			return nil, fmt.Errorf("database query failed: %w", err)
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

// createStatusFilter generates the OR filter for relevant schedule statuses.
func createStatusFilter() notionapi.OrCompoundFilter {
	// Statuses indicating the task is not yet Done or Archived
	relevantStatuses := []string{"CannotDo", "Next", "Want", "ToDo", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday", "Doing"} // Added "Doing"
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

// parseNotionPage converts a Notion page object into a Task struct.
func parseNotionPage(page notionapi.Page) *Task {
	task := Task{
		ID:         page.ID,
		URL:        page.URL,
		NotifyDays: defaultNotifyDays, // Default notification days
	}

	// Iterate over properties safely
	for propName, propValue := range page.Properties {
		switch propName {
		case nameProp:
			if p, ok := propValue.(*notionapi.TitleProperty); ok && len(p.Title) > 0 {
				task.Title = p.Title[0].Text.Content
			}
		case dueProp:
			if p, ok := propValue.(*notionapi.DateProperty); ok && p.Date != nil {
				task.DueStart = p.Date.Start
				task.DueEnd = p.Date.End // Can be nil if it's just a date without time or end date
			}
		case priorityProp:
			if p, ok := propValue.(*notionapi.SelectProperty); ok && p.Select.Name != "" { // Check Name instead of nil
				task.Priority = p.Select.Name
			}
		case typeProp:
			if p, ok := propValue.(*notionapi.SelectProperty); ok && p.Select.Name != "" { // Check Name instead of nil
				task.Type = p.Select.Name
			}
		case scheduleStatusProp:
			if p, ok := propValue.(*notionapi.StatusProperty); ok && p.Status.Name != "" { // Check Name instead of nil
				task.ScheduleStatus = p.Status.Name
			}
		case workloadProp:
			if p, ok := propValue.(*notionapi.SelectProperty); ok && p.Select.Name != "" { // Check Name instead of nil
				task.Workload = p.Select.Name
			}
		case memoProp:
			// Memo might be empty or have multiple rich text parts
			if p, ok := propValue.(*notionapi.RichTextProperty); ok && len(p.RichText) > 0 {
				var memoBuilder strings.Builder
				for i, rt := range p.RichText {
					if i > 0 {
						memoBuilder.WriteString("\n") // Add newline between parts if needed
					}
					memoBuilder.WriteString(rt.Text.Content)
				}
				task.Memo = memoBuilder.String()
			}
		case notifyDaysProp:
			if p, ok := propValue.(*notionapi.SelectProperty); ok && p.Select.Name != "" { // Check Name instead of nil
				if days, err := strconv.Atoi(p.Select.Name); err == nil && (days == 1 || days == 3 || days == 7) {
					task.NotifyDays = days
				} else {
					log.Printf("Warning: Invalid value '%s' in '%s' for task '%s'. Using default %d days.",
						p.Select.Name, notifyDaysProp, task.Title, defaultNotifyDays)
				}
			}
		}
	}

	// Basic validation: Title and Due date are essential
	if task.Title == "" || (task.DueStart == nil && task.DueEnd == nil) {
		log.Printf("Warning: Skipping page ID %s due to missing Title or Due date.", page.ID)
		return nil
	}

	return &task
}

// filterTasks filters the fetched tasks based on notification rules and current time.
func filterTasks(tasks []Task, now time.Time) []Task {
	var filtered []Task
	currentHour := now.Hour()
	isUrgentNotificationTime := (currentHour == 13 || currentHour == 20) // 1 PM or 8 PM

	for _, task := range tasks {
		if shouldNotify(task, now) {
			// If it's 1 PM or 8 PM, only include tasks due *today*
			if isUrgentNotificationTime {
				if isDueToday(task, now) {
					filtered = append(filtered, task)
				}
			} else {
				// Otherwise (e.g., 9 AM), include all tasks that meet the notification criteria
				filtered = append(filtered, task)
			}
		}
	}
	return filtered
}

// shouldNotify determines if a task notification should be sent based on its due date and custom settings.
func shouldNotify(task Task, now time.Time) bool {
	targetDate := getTargetDueDate(task)
	if targetDate == nil {
		return false // Should not happen due to earlier checks, but safety first
	}

	// Calculate days remaining (considering only the date part)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dueDate := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, now.Location())

	// If due date is in the past, don't notify (unless it's today)
	if dueDate.Before(today) {
		return false
	}

	daysRemaining := int(dueDate.Sub(today).Hours() / 24)

	// Notify if days remaining is less than or equal to the configured notification days
	return daysRemaining <= task.NotifyDays
}

// isDueToday checks if the task's target due date is today.
func isDueToday(task Task, now time.Time) bool {
	targetDate := getTargetDueDate(task)
	if targetDate == nil {
		return false
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dueDate := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, now.Location())
	return dueDate.Equal(today)
}

// getTargetDueDate returns the effective due date (End preferred, then Start).
func getTargetDueDate(task Task) *time.Time {
	if task.DueEnd != nil {
		t := time.Time(*task.DueEnd)
		return &t
	}
	if task.DueStart != nil {
		t := time.Time(*task.DueStart)
		return &t
	}
	return nil // Should have a due date based on query filters
}

// formatDueDate formats the due date for display.
func formatDueDate(task Task) string {
	layout := "2006-01-02" // Date only layout
	if task.DueEnd != nil {
		// If End exists, check if Start also exists and is different
		// Use time.Time().Equal() for comparison
		if task.DueStart != nil && !time.Time(*task.DueStart).Equal(time.Time(*task.DueEnd)) {
			// Check if they are on the same day, format time if they are
			startT := time.Time(*task.DueStart)
			endT := time.Time(*task.DueEnd)
			if startT.Year() == endT.Year() && startT.Month() == endT.Month() && startT.Day() == endT.Day() {
				timeLayout := "15:04" // Time only layout if available
				startStr := ""
				endStr := ""
				if !startT.IsZero() && startT.Hour() != 0 || startT.Minute() != 0 { // Check if time part is meaningful
					startStr = startT.Format(timeLayout)
				}
				if !endT.IsZero() && endT.Hour() != 0 || endT.Minute() != 0 { // Check if time part is meaningful
					endStr = endT.Format(timeLayout)
				}
				if startStr != "" && endStr != "" {
					return fmt.Sprintf("%s (%s ~ %s)", endT.Format(layout), startStr, endStr)
				} else if endStr != "" { // Only end time meaningful
					return fmt.Sprintf("%s (~%s)", endT.Format(layout), endStr)
				} else if startStr != "" { // Only start time meaningful
					return fmt.Sprintf("%s (%s~)", endT.Format(layout), startStr)
				}
				// Fallback if times are zero
				return endT.Format(layout)

			} else {
				// Different days
				return fmt.Sprintf("%s ~ %s", startT.Format(layout), endT.Format(layout))
			}
		}
		// Only End date exists or Start is the same as End
		return time.Time(*task.DueEnd).Format(layout)
	}
	// Only Start date exists
	if task.DueStart != nil {
		return time.Time(*task.DueStart).Format(layout)
	}
	return "N/A" // Should not happen
}

// --- Slack Formatting & Sending ---

// buildSlackBlocks creates the Slack message blocks.
func buildSlackBlocks(tasks []Task, now time.Time) []slack.Block {
	// Group tasks by urgency
	todayTasks, threeDayTasks, sevenDayTasks := groupTasksByUrgency(tasks, now)

	// Sort tasks within each group
	sortTasks(todayTasks)
	sortTasks(threeDayTasks)
	sortTasks(sevenDayTasks)

	var blocks []slack.Block

	// Header
	blocks = append(blocks, slack.NewHeaderBlock(slack.NewTextBlockObject(slack.PlainTextType, "🔔 Notion Task Reminders", true, false)))

	// Add sections for each group if they have tasks
	blocks = appendSection(blocks, "🚨 Due Today", todayTasks)
	blocks = appendSection(blocks, "⚠️ Due Within 3 Days", threeDayTasks)
	blocks = appendSection(blocks, "🗓️ Due Within 7 Days", sevenDayTasks)

	// Footer
	blocks = append(blocks, slack.NewDividerBlock())
	blocks = append(blocks, slack.NewContextBlock("", slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("Generated at %s", now.Format(time.RFC1123)), false, false)))

	return blocks
}

// groupTasksByUrgency categorizes tasks based on their due date relative to now.
func groupTasksByUrgency(tasks []Task, now time.Time) (today, threeDays, sevenDays []Task) {
	todayBoundary := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	threeDaysBoundary := todayBoundary.AddDate(0, 0, 3)
	sevenDaysBoundary := todayBoundary.AddDate(0, 0, 7) // Although initial fetch is 7 days, group explicitly

	for _, task := range tasks {
		dueDate := getTargetDueDate(task)
		if dueDate == nil {
			continue
		}
		dueDateTime := time.Date(dueDate.Year(), dueDate.Month(), dueDate.Day(), 0, 0, 0, 0, now.Location())

		if !dueDateTime.After(todayBoundary) { // Due today or overdue (overdue shouldn't happen based on filter)
			today = append(today, task)
		} else if !dueDateTime.After(threeDaysBoundary) { // Due within 1-3 days
			threeDays = append(threeDays, task)
		} else if !dueDateTime.After(sevenDaysBoundary) { // Due within 4-7 days
			sevenDays = append(sevenDays, task)
		}
		// Tasks due later than 7 days are already filtered out
	}
	return
}

// sortTasks sorts tasks first by priority (High > Medium > Low > None), then by due date.
func sortTasks(tasks []Task) {
	sort.SliceStable(tasks, func(i, j int) bool {
		priI := priorityOrder[tasks[i].Priority]
		priJ := priorityOrder[tasks[j].Priority]
		if priI != priJ {
			return priI < priJ // Lower number means higher priority
		}
		// If priorities are the same, sort by due date (earlier first)
		dueI := getTargetDueDate(tasks[i])
		dueJ := getTargetDueDate(tasks[j])
		if dueI != nil && dueJ != nil {
			return dueI.Before(*dueJ)
		}
		// Handle nil cases (shouldn't happen ideally)
		return dueI != nil
	})
}

// appendSection adds a formatted section for a task group to the Slack blocks.
func appendSection(blocks []slack.Block, title string, tasks []Task) []slack.Block {
	if len(tasks) == 0 {
		return blocks // Don't add empty sections
	}

	blocks = append(blocks, slack.NewDividerBlock())
	blocks = append(blocks, slack.NewSectionBlock(
		slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("*%s*", title), false, false),
		nil, nil),
	)

	for _, task := range tasks {
		taskText := fmt.Sprintf("*<%s|%s>*", task.URL, task.Title) // Link + Title

		var details []string
		details = append(details, fmt.Sprintf("*Due:* %s", formatDueDate(task)))
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
			details = append(details, fmt.Sprintf("*Priority:* %s%s", priorityEmoji, task.Priority))
		}
		if task.Type != "" {
			details = append(details, fmt.Sprintf("*Type:* %s", task.Type))
		}
		if task.ScheduleStatus != "" {
			details = append(details, fmt.Sprintf("*Status:* %s", task.ScheduleStatus))
		}
		if task.Workload != "" {
			details = append(details, fmt.Sprintf("*Workload:* %s", task.Workload))
		}
		// Add Memo if it exists, truncate if too long for Slack block
		if task.Memo != "" {
			maxMemoLength := 150 // Limit memo length in the main block
			truncatedMemo := task.Memo
			if len(truncatedMemo) > maxMemoLength {
				truncatedMemo = truncatedMemo[:maxMemoLength] + "..."
			}
			// Escape markdown characters in memo
			escapedMemo := strings.ReplaceAll(truncatedMemo, "*", "\\*")
			escapedMemo = strings.ReplaceAll(escapedMemo, "_", "\\_")
			escapedMemo = strings.ReplaceAll(escapedMemo, "~", "\\~")
			escapedMemo = strings.ReplaceAll(escapedMemo, "`", "\\`")

			details = append(details, fmt.Sprintf("*Memo:* %s", escapedMemo))
		}

		// Combine details, ensuring the total length doesn't exceed Slack limits (approx 3000 chars per field)
		detailsText := strings.Join(details, " | ")
		if len(detailsText) > 2900 { // Leave some buffer
			detailsText = detailsText[:2900] + "..."
		}

		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, taskText+"\n"+detailsText, false, false),
			nil, nil), // No fields or accessory needed here for now
		)
	}

	return blocks
}

// formatSlackMessage creates a simple text fallback message (less used with Block Kit).
func formatSlackMessage(tasks []Task, now time.Time) string {
	// This is mainly a fallback if blocks fail to render
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("Notion Task Reminders (%s)\n", now.Format("2006-01-02")))

	today, threeDays, sevenDays := groupTasksByUrgency(tasks, now)
	sortTasks(today)
	sortTasks(threeDays)
	sortTasks(sevenDays)

	appendTasksToString(&builder, "Due Today", today)
	appendTasksToString(&builder, "Due Within 3 Days", threeDays)
	appendTasksToString(&builder, "Due Within 7 Days", sevenDays)

	return builder.String()
}

// appendTasksToString helper for the fallback text message.
func appendTasksToString(builder *strings.Builder, title string, tasks []Task) {
	if len(tasks) > 0 {
		builder.WriteString(fmt.Sprintf("\n*%s*\n", title))
		for _, task := range tasks {
			builder.WriteString(fmt.Sprintf("- *%s* (Due: %s", task.Title, formatDueDate(task)))
			if task.Priority != "" {
				builder.WriteString(fmt.Sprintf(", Priority: %s", task.Priority))
			}
			builder.WriteString(fmt.Sprintf(") <%s>\n", task.URL))
		}
	}
}
