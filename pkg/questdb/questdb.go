package questdb

import (
	"context"
	"fmt"

	qdb "github.com/questdb/go-questdb-client/v4"
)

type Client struct {
	Sender qdb.LineSender
}

func NewClient(cfg *QuestDBConfig) (*Client, error) {
	sender, err := qdb.NewLineSender(context.Background(), qdb.WithTcp(), qdb.WithAddress(fmt.Sprintf("%s:%s", cfg.Host, cfg.Port)))
	if err != nil {
		return nil, err
	}

	return &Client{Sender: sender}, nil
}

func (c *Client) Flush(ctx context.Context) error {
	return c.Sender.Flush(ctx)
}

func (c *Client) Close() {
	c.Sender.Close(context.Background())
}
