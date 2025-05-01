package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jomei/notionapi"
)

func main() {
	NOTION_TOKEN := os.Getenv("NOTION_TOKEN")
	if NOTION_TOKEN == "" {
		fmt.Printf("not found notion api key")
		return
	}

	client := notionapi.NewClient(notionapi.Token(NOTION_TOKEN))
	dbID := "70c9526ec8e54c7b9ebbe48f21c4c429"
	ctx := context.Background()
	request := &notionapi.DatabaseQueryRequest{
		// TODO: 追加でフィルターを増やす
		Filter: &notionapi.OrCompoundFilter{
			&notionapi.PropertyFilter{
				Property: "Schedule Status",
				Status: &notionapi.StatusFilterCondition{
					Equals: "Friday",
				},
			},
			&notionapi.PropertyFilter{
				Property: "Type",
				Select: &notionapi.SelectFilterCondition{
					Equals: "Monday",
				},
			},
		},
		// TODO: Sortが正常に動いているかチェック
		Sorts: []notionapi.SortObject{
			{
				Property:  "Due",
				Direction: "descending",
			},
		},
		PageSize: 10,
	}
	page, err := client.Database.Query(ctx, notionapi.DatabaseID(dbID), request)
	if err != nil {
		fmt.Println(err)
	}
	for _, page := range page.Results {

		title := page.Properties["Name"].(*notionapi.TitleProperty).Title[0].Text.Content
		// start_time := page.Properties["Due"].(*notionapi.DateProperty).Date.Start
		// end_time := page.Properties["Due"].(*notionapi.DateProperty).Date.End
		priority := page.Properties["Priority"].(*notionapi.SelectProperty).Select.Name
		task_type := page.Properties["Type"].(*notionapi.SelectProperty).Select.Name
		// memo := page.Properties["Memo"].(*notionapi.RichTextProperty).RichText[0].Text.Content

		fmt.Printf("Title: %s\n", title)
		// fmt.Printf("Due Date: %s - %s\n", start_time, end_time)
		fmt.Printf("Priority: %s\n", priority)
		fmt.Printf("Type: %s\n", task_type)
		// fmt.Printf("Memo: %s\n", memo)
		fmt.Println()
	}
}
