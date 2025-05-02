package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jomei/notionapi"
)

func main() {
	NOTION_TOKEN := os.Getenv("NOTION_TOKEN")
	if NOTION_TOKEN == "" {
		fmt.Printf("not found notion api key")
		return
	}

	client := notionapi.NewClient(notionapi.Token(NOTION_TOKEN))
	// タスクDBのDB ID
	dbID := "70c9526ec8e54c7b9ebbe48f21c4c429"

	ctx := context.Background()

	threeDay := time.Now().AddDate(0, 0, 3)
	request := &notionapi.DatabaseQueryRequest{
		// 今より3日後以前のタスクを取得
		Filter: &notionapi.AndCompoundFilter{
			&notionapi.PropertyFilter{
				Property: "Due",
				Date: &notionapi.DateFilterCondition{
					Before: (*notionapi.Date)(&threeDay),
				},
			},
			// Schedule Statusが"CannotDo", "Next", "Want", "ToDo", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"のいずれかであること
			func() notionapi.OrCompoundFilter {
				statuses := []string{"CannotDo", "Next", "Want", "ToDo", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
				var filter []notionapi.Filter
				for _, status := range statuses {
					filter = append(filter, &notionapi.PropertyFilter{
						Property: "Schedule Status",
						Status: &notionapi.StatusFilterCondition{
							Equals: status,
						},
					})
				}
				orFilter := notionapi.OrCompoundFilter(filter)
				return orFilter
			}(),
		},
		Sorts: []notionapi.SortObject{
			// Dueの昇順
			{
				Property:  "Due",
				Direction: "ascending",
			},
			// Priorityの昇順
			// これは、Priorityが"High"、"Medium"、"Low"の順に並ぶようにするため
			{
				Property:  "Priority",
				Direction: "ascending",
			},
		},
		PageSize: 20,
	}
	page, err := client.Database.Query(ctx, notionapi.DatabaseID(dbID), request)
	if err != nil {
		fmt.Println(err)
	}
	for _, page := range page.Results {

		title := page.Properties["Name"].(*notionapi.TitleProperty).Title[0].Text.Content

		var start_time *notionapi.Date
		var end_time *notionapi.Date
		if page.Properties["Due"].(*notionapi.DateProperty).Date != nil {
			start_time = page.Properties["Due"].(*notionapi.DateProperty).Date.Start
		}

		if page.Properties["Due"].(*notionapi.DateProperty).Date != nil {
			end_time = page.Properties["Due"].(*notionapi.DateProperty).Date.End
		}
		priority := page.Properties["Priority"].(*notionapi.SelectProperty).Select.Name
		task_type := page.Properties["Type"].(*notionapi.SelectProperty).Select.Name
		// memo := page.Properties["Memo"].(*notionapi.RichTextProperty).RichText[0].Text.Content

		fmt.Printf("Title: %s\n", title)
		fmt.Printf("Due Date: %s - %s\n", start_time, end_time)
		fmt.Printf("Priority: %s\n", priority)
		fmt.Printf("Type: %s\n", task_type)
		// fmt.Printf("Memo: %s\n", memo)
		fmt.Println()
	}
}
