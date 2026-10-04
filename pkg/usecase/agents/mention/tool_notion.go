package mention

import (
	"context"
	"encoding/json"

	"github.com/m-mizutani/robin/pkg/domain/model"
	"github.com/m-mizutani/robin/pkg/usecase"
)

// notionPageSize is the number of results one Notion list call returns.
const notionPageSize = 25

type notionListResult struct {
	Results    []json.RawMessage `json:"results"`
	NextCursor string            `json:"next_cursor,omitempty"`
	HasMore    bool              `json:"has_more"`
}

func notionListJSON(list *model.NotionList) (string, error) {
	return toJSON(notionListResult{Results: list.Results, NextCursor: list.NextCursor, HasMore: list.HasMore})
}

type notionSearchInput struct {
	Query       string `json:"query"`
	ObjectType  string `json:"object_type"`
	StartCursor string `json:"start_cursor"`
}

type notionIDInput struct {
	PageID       string `json:"page_id"`
	BlockID      string `json:"block_id"`
	DatabaseID   string `json:"database_id"`
	DataSourceID string `json:"data_source_id"`
	StartCursor  string `json:"start_cursor"`
}

type notionQueryInput struct {
	DataSourceID string          `json:"data_source_id"`
	Filter       json.RawMessage `json:"filter"`
	Sorts        json.RawMessage `json:"sorts"`
	StartCursor  string          `json:"start_cursor"`
}

// notionObjectID checks a required Notion ID.
func notionObjectID(name, value string) (model.NotionObjectID, error) {
	if err := requireString(name, value); err != nil {
		return "", err
	}
	id := model.NotionObjectID(value)
	if id.Validate() != nil {
		return "", inputError("Invalid input: %s must be a Notion ID (a UUID).", name)
	}
	return id, nil
}

// omitNull treats an explicit null like an omitted field.
func omitNull(raw json.RawMessage) json.RawMessage {
	if string(raw) == "null" {
		return nil
	}
	return raw
}

// notionRawJSON passes a Notion object to the model as Notion returned it.
func notionRawJSON(raw json.RawMessage, err error) (string, error) {
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func notionList(list *model.NotionList, err error) (string, error) {
	if err != nil {
		return "", err
	}
	return notionListJSON(list)
}

const cursorProperty = `"start_cursor":{"type":"string","description":"next_cursor of the previous result, to read the next page."}`

func notionTools(notion NotionReader) []*agentTool {
	idTool := func(name, description, idName, idDescription, progress string,
		call func(ctx context.Context, key model.UserKey, in notionIDInput) (string, error)) *agentTool {
		return &agentTool{
			service: "Notion",
			spec: modelToolSpec(name, description,
				`{"type":"object","properties":{"`+idName+`":{"type":"string","description":"`+idDescription+`"}},"required":["`+idName+`"]}`),
			describe: func(json.RawMessage) string { return progress },
			run: func(ctx context.Context, req usecase.MentionRequest, input json.RawMessage) (string, error) {
				in, err := decodeInput[notionIDInput](input)
				if err != nil {
					return "", err
				}
				return call(ctx, req.Key, in)
			},
		}
	}

	return []*agentTool{
		{
			service: "Notion",
			spec: modelToolSpec("notion_search",
				"Search the Notion pages and data sources the requester shared with Robin, by title. An empty query lists all of them.",
				`{"type":"object","properties":{
					"query":{"type":"string","description":"Words in the title."},
					"object_type":{"type":"string","enum":["page","data_source"],"description":"Return only this kind of object."},
					`+cursorProperty+`
				}}`),
			describe: func(input json.RawMessage) string {
				in, _ := decodeInput[notionSearchInput](input)
				return "Searching Notion for " + quoted(in.Query)
			},
			run: func(ctx context.Context, req usecase.MentionRequest, input json.RawMessage) (string, error) {
				in, err := decodeInput[notionSearchInput](input)
				if err != nil {
					return "", err
				}
				objectType := model.NotionObjectType(in.ObjectType)
				if objectType.Validate() != nil {
					return "", inputError("Invalid input: object_type must be \"page\" or \"data_source\".")
				}
				return notionList(notion.Search(ctx, req.Key, model.NotionSearchQuery{
					Query:      in.Query,
					ObjectType: objectType,
					Page:       model.NotionPagination{StartCursor: in.StartCursor, PageSize: notionPageSize},
				}))
			},
		},
		idTool("notion_get_page", "Read the properties of a Notion page. Read its content with notion_get_block_children.",
			"page_id", "Page ID from a search result.", "Reading a Notion page",
			func(ctx context.Context, key model.UserKey, in notionIDInput) (string, error) {
				id, err := notionObjectID("page_id", in.PageID)
				if err != nil {
					return "", err
				}
				return notionRawJSON(notion.GetPage(ctx, key, id))
			}),
		{
			service: "Notion",
			spec: modelToolSpec("notion_get_block_children",
				"Read the blocks (the content) directly under a Notion page or block. A block with has_children true has more blocks under it.",
				`{"type":"object","properties":{
					"block_id":{"type":"string","description":"Page ID or block ID."},
					`+cursorProperty+`
				},"required":["block_id"]}`),
			describe: func(json.RawMessage) string { return "Reading Notion page content" },
			run: func(ctx context.Context, req usecase.MentionRequest, input json.RawMessage) (string, error) {
				in, err := decodeInput[notionIDInput](input)
				if err != nil {
					return "", err
				}
				id, err := notionObjectID("block_id", in.BlockID)
				if err != nil {
					return "", err
				}
				return notionList(notion.ListBlockChildren(ctx, req.Key, id,
					model.NotionPagination{StartCursor: in.StartCursor, PageSize: notionPageSize}))
			},
		},
		idTool("notion_get_database", "Read a Notion database and the list of its data sources.",
			"database_id", "Database ID.", "Reading a Notion database",
			func(ctx context.Context, key model.UserKey, in notionIDInput) (string, error) {
				id, err := notionObjectID("database_id", in.DatabaseID)
				if err != nil {
					return "", err
				}
				return notionRawJSON(notion.GetDatabase(ctx, key, id))
			}),
		idTool("notion_get_data_source", "Read a Notion data source with its property schema. Read it before querying the data source.",
			"data_source_id", "Data source ID.", "Reading a Notion data source",
			func(ctx context.Context, key model.UserKey, in notionIDInput) (string, error) {
				id, err := notionObjectID("data_source_id", in.DataSourceID)
				if err != nil {
					return "", err
				}
				return notionRawJSON(notion.GetDataSource(ctx, key, id))
			}),
		{
			service: "Notion",
			spec: modelToolSpec("notion_query_data_source",
				"Read the rows of a Notion data source, with Notion's filter and sorts objects.",
				`{"type":"object","properties":{
					"data_source_id":{"type":"string","description":"Data source ID."},
					"filter":{"type":"object","description":"Notion filter object."},
					"sorts":{"type":"array","items":{"type":"object"},"description":"Notion sorts array."},
					`+cursorProperty+`
				},"required":["data_source_id"]}`),
			describe: func(json.RawMessage) string { return "Querying a Notion data source" },
			run: func(ctx context.Context, req usecase.MentionRequest, input json.RawMessage) (string, error) {
				in, err := decodeInput[notionQueryInput](input)
				if err != nil {
					return "", err
				}
				id, err := notionObjectID("data_source_id", in.DataSourceID)
				if err != nil {
					return "", err
				}
				return notionList(notion.QueryDataSource(ctx, req.Key, id, model.NotionDataSourceQuery{
					Filter: omitNull(in.Filter),
					Sorts:  omitNull(in.Sorts),
					Page:   model.NotionPagination{StartCursor: in.StartCursor, PageSize: notionPageSize},
				}))
			},
		},
	}
}
