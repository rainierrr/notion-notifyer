package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/jomei/notionapi"
)

var SCHEDULE_STATUSES = []string{
	"CannotDo", "Next", "Want", "ToDo", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday", "Doing", "iPhone Task",
}

func fetchNotionTasks(ctx context.Context, client *notionapi.Client, dbID string, onOrBeforeDate time.Time) ([]Task, error) {
	var allTasks []Task

	request := &notionapi.DatabaseQueryRequest{
		Filter: &notionapi.AndCompoundFilter{
			&notionapi.PropertyFilter{
				Property: dueProp,
				Date: &notionapi.DateFilterCondition{
					OnOrBefore: (*notionapi.Date)(&onOrBeforeDate),
				},
			},
			createStatusFilter(),
		},
		Sorts: []notionapi.SortObject{
			{Property: dueProp, Direction: notionapi.SortOrderASC},      // 期限日でソート
			{Property: priorityProp, Direction: notionapi.SortOrderASC}, // ステータスでソート
		},
	}

	resp, err := client.Database.Query(ctx, notionapi.DatabaseID(dbID), request)
	if err != nil {
		return nil, fmt.Errorf("failed to query database: %w", err)
	}

	for _, page := range resp.Results {
		task := parseNotionPage(page)
		// 開始日と終了日が両方とも設定されている場合、Notion APIでは開始日が優先的にフィルターに利用されるため、終了日をチェックする
		if task.DueEnd != nil && time.Time(*task.DueEnd).After(onOrBeforeDate) {
			continue
		}
		if task != nil {
			allTasks = append(allTasks, *task)
		}
	}

	return allTasks, nil
}

func createStatusFilter() notionapi.OrCompoundFilter {
	var filters []notionapi.Filter
	for _, status := range SCHEDULE_STATUSES {
		filters = append(filters, &notionapi.PropertyFilter{
			Property: scheduleStatusProp,
			Status: &notionapi.StatusFilterCondition{
				Equals: status,
			},
		})
	}
	return notionapi.OrCompoundFilter(filters)
}

// Notion ページを Task 構造体に変換する
func parseNotionPage(page notionapi.Page) *Task {
	task := Task{
		ID:  page.ID,
		URL: page.URL,
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
				task.DueEnd = p.Date.End
			}
		case priorityProp:
			if p, ok := propValue.(*notionapi.SelectProperty); ok && p.Select.Name != "" {
				task.Priority = p.Select.Name
			}
		case typeProp:
			if p, ok := propValue.(*notionapi.SelectProperty); ok && p.Select.Name != "" {
				task.Type = p.Select.Name
			}
		case scheduleStatusProp:
			if p, ok := propValue.(*notionapi.StatusProperty); ok && p.Status.Name != "" {
				task.ScheduleStatus = p.Status.Name
			}
		case workloadProp:
			if p, ok := propValue.(*notionapi.SelectProperty); ok && p.Select.Name != "" {
				workload, err := strconv.ParseFloat(p.Select.Name, 32)
				if err == nil {
					task.Workload = float32(workload)
				} else {
					log.Printf("Warning: Unable to parse workload for task ID %s: %v", task.ID, err)
				}
			}
		case memoProp:
			if p, ok := propValue.(*notionapi.RichTextProperty); ok && len(p.RichText) > 0 {
				var memoBuilder strings.Builder
				for i, rt := range p.RichText {
					if i > 0 {
						memoBuilder.WriteString("\n")
					}
					memoBuilder.WriteString(rt.Text.Content)
				}
				task.Memo = memoBuilder.String()
			}
		}
	}

	// 必須プロパティの検証: タイトルと期限日は必須
	if task.Title == "" || (task.DueStart == nil && task.DueEnd == nil) {
		log.Printf("Warning: Task with ID %s is missing required properties (Title or Due Date). Skipping.", task.ID)
		return nil
	}

	return &task
}
