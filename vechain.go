package xk6_vechain

import (
	"errors"
	"math/big"
	"strconv"
	"sync"
	"time"

	"github.com/darrenvechain/thor-go-sdk/builtins"
	"github.com/darrenvechain/thor-go-sdk/crypto/hdwallet"
	"github.com/darrenvechain/thor-go-sdk/crypto/transaction"
	"github.com/darrenvechain/thor-go-sdk/thorgo"
	"github.com/darrenvechain/thor-go-sdk/txmanager"
	"github.com/darrenvechain/xk6-vechain/toolchain"
	"github.com/ethereum/go-ethereum/common"
	"go.k6.io/k6/js/modules"
	"go.k6.io/k6/metrics"
)

type Client struct {
	wallet   *hdwallet.Wallet
	thor     *thorgo.Thor
	chainTag byte
	vu       modules.VU
	metrics  vechainMetrics
	opts     *options
	accounts int
	managers []*txmanager.PKManager
}

func (c *Client) Accounts() []string {
	addresses := make([]string, 0)
	for _, i := range c.managers {
		addresses = append(addresses, i.Address().String())
	}
	return addresses
}

func (c *Client) DeployToolchain(amount int) ([]string, error) {
	contracts, err := toolchain.Deploy(c.thor, c.managers, amount)
	if err != nil {
		return nil, err
	}
	addresses := make([]string, 0)
	for _, contract := range contracts {
		addresses = append(addresses, contract.Address.String())
	}
	return addresses, nil
}

func (c *Client) NewToolchainTransaction(address string) (string, error) {
	addr := common.HexToAddress(address)
	return toolchain.NewTransaction(c.thor, c.managers, addr)
}

// Fund sends VET and VTHO to the accounts after the index, funded by the accounts before the index.
// The amount is the amount of VET & VTHO to send, represented as hex.
// Example: thor solo only funds the first 10 accounts [0-9], so specify 10 as the start index.
func (c *Client) Fund(start int, amount string) error {
	if start > len(c.managers) {
		return errors.New("start index is greater than the number of accounts")
	}

	// funder index -> clauses to send
	clauses := make(map[int][]*transaction.Clause)
	vtho := builtins.VTHO.Load(c.thor)

	for i := start; i < len(c.managers); i++ {
		fundee := c.managers[i].Address()
		funderIndex := i % start

		value := new(big.Int)
		value.SetString(amount, 16)

		vetClause := transaction.NewClause(&fundee).WithValue(value)
		vthoClause, err := vtho.AsClause("transfer", fundee, value)
		if err != nil {
			return err
		}

		funderClauses := clauses[funderIndex]
		if funderClauses == nil {
			funderClauses = make([]*transaction.Clause, 0)
		}

		clauses[funderIndex] = append(funderClauses, vetClause, vthoClause)
	}

	var (
		wg        sync.WaitGroup
		clauseErr error
	)

	for i, clauses := range clauses {
		wg.Add(1)
		manager := c.managers[i]
		go func(i *txmanager.PKManager, clauses []*transaction.Clause) {
			defer wg.Done()
			for i := 0; i < len(clauses); i += 100 {
				end := i + 100
				if end > len(clauses) {
					end = len(clauses)
				}

				tx, err := c.thor.Transactor(clauses[i:end], manager.Address()).Send(manager)
				if err != nil {
					clauseErr = err
					return
				}

				_, err = tx.Wait()
				if err != nil {
					clauseErr = err
					return
				}
			}
		}(manager, clauses)
	}

	wg.Wait()

	if clauseErr != nil {
		return clauseErr
	}

	return nil
}

var blocks sync.Map

func (c *Client) pollForBlocks() {
	prev, err := c.thor.Blocks.Best()
	if err != nil {
		return
	}

	for range time.Tick(500 * time.Millisecond) {
		block, err := c.thor.Blocks.Best()
		if err != nil {
			continue
		}

		if block.Number > prev.Number {
			blockTimestampDiff := time.Unix(int64(block.Timestamp), 0).Sub(time.Unix(int64(prev.Timestamp), 0))
			tps := float64(len(block.Transactions)) / float64(blockTimestampDiff.Seconds())

			prev = block

			rootTS := metrics.NewRegistry().RootTagSet()
			if c.vu != nil && c.vu.State() != nil && rootTS != nil {
				if _, loaded := blocks.LoadOrStore(c.opts.URL+strconv.FormatUint(block.Number, 10), true); loaded {
					// We already have a block number for this client, so we can skip this
					continue
				}

				metrics.PushIfNotDone(c.vu.Context(), c.vu.State().Samples, metrics.ConnectedSamples{
					Samples: []metrics.Sample{
						{
							TimeSeries: metrics.TimeSeries{
								Metric: c.metrics.Block,
								Tags: rootTS.WithTagsFromMap(map[string]string{
									"transactions": strconv.Itoa(len(block.Transactions)),
									"gas_used":     strconv.Itoa(int(block.GasUsed)),
									"gas_limit":    strconv.Itoa(int(block.GasLimit)),
								}),
							},
							Value: float64(block.Number),
							Time:  time.Now(),
						},
						{
							TimeSeries: metrics.TimeSeries{
								Metric: c.metrics.GasUsed,
								Tags: rootTS.WithTagsFromMap(map[string]string{
									"block": strconv.Itoa(int(block.Number)),
								}),
							},
							Value: float64(block.GasUsed),
							Time:  time.Now(),
						},
						{
							TimeSeries: metrics.TimeSeries{
								Metric: c.metrics.TPS,
								Tags:   rootTS,
							},
							Value: tps,
							Time:  time.Now(),
						},
						{
							TimeSeries: metrics.TimeSeries{
								Metric: c.metrics.BlockTime,
								Tags: rootTS.WithTagsFromMap(map[string]string{
									"block_timestamp_diff": blockTimestampDiff.String(),
								}),
							},
							Value: float64(blockTimestampDiff.Milliseconds()),
							Time:  time.Now(),
						},
					},
				})
			}
		}
	}
}
