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
		// Filter:   &notionapi.PropertyFilter{},
		PageSize: 10,
	}
	page, err := client.Database.Query(ctx, notionapi.DatabaseID(dbID), request)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Printf("%+v", page)
}
