package xk6_vechain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/darrenvechain/thorgo"
	"github.com/darrenvechain/thorgo/crypto/hdwallet"
	"github.com/darrenvechain/thorgo/txmanager"
	"github.com/grafana/sobek"
	"github.com/vechain/xk6-vechain/accounts"
	"go.k6.io/k6/js/common"
	"go.k6.io/k6/js/modules"
	"go.k6.io/k6/metrics"
)

const (
	mnemonic      = "denial kitchen pet squirrel other broom bar gas better priority spoil cross"
	accountAmount = 10
)

type vechainMetrics struct {
	RequestDuration *metrics.Metric
	TimeToMine      *metrics.Metric
	Block           *metrics.Metric
	GasUsed         *metrics.Metric
	TPS             *metrics.Metric
	BlockTime       *metrics.Metric
	BaseFee         *metrics.Metric
}

func init() {
	modules.Register("k6/x/vechain", &EthRoot{})
	modules.Register("k6/x/vechain/accounts", &accounts.Account{})
}

// EthRoot is the root module
type EthRoot struct{}

// NewModuleInstance implements the modules.Module interface returning a new instance for each VU.
func (*EthRoot) NewModuleInstance(vu modules.VU) modules.Instance {
	return &ModuleInstance{
		vu: vu,
		m:  registerMetrics(vu),
	}
}

type ModuleInstance struct {
	vu modules.VU
	m  vechainMetrics
}

// Exports implements the modules.Instance interface and returns the exported types for the JS module.
func (mi *ModuleInstance) Exports() modules.Exports {
	return modules.Exports{Named: map[string]interface{}{
		"Client": mi.NewClient,
	}}
}

func (mi *ModuleInstance) NewClient(call sobek.ConstructorCall) *sobek.Object {
	rt := mi.vu.Runtime()

	var optionsArg map[string]interface{}
	err := rt.ExportTo(call.Arguments[0], &optionsArg)
	if err != nil {
		common.Throw(rt, errors.New("unable to parse options object"))
	}

	opts, err := newOptionsFrom(optionsArg)
	if err != nil {
		common.Throw(rt, fmt.Errorf("invalid options; reason: %w", err))
	}

	if opts.URL == "" {
		opts.URL = "http://localhost:8669"
	}

	if opts.Mnemonic == "" {
		opts.Mnemonic = mnemonic
	}

	if opts.Accounts == 0 {
		opts.Accounts = accountAmount
	}

	wa, err := hdwallet.FromMnemonic(opts.Mnemonic)
	if err != nil {
		common.Throw(rt, fmt.Errorf("invalid options; reason: %w", err))
	}

	thor := thorgo.New(context.Background(), opts.URL)

	chainTag, err := thor.Client().ChainTag()
	if err != nil {
		common.Throw(rt, fmt.Errorf("failed to get chain tag: %w", err))
	}

	managers := make([]*txmanager.PKManager, opts.Accounts)
	for i := 0; i < opts.Accounts; i++ {
		key, err := wa.Child(uint32(i))
		if err != nil {
			panic(err)
		}
		manager := txmanager.FromPK(key.MustGetPrivateKey(), thor.Client())
		managers[i] = manager
	}

	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{
		vu:          mi.vu,
		metrics:     mi.m,
		thor:        thor,
		wallet:      wa,
		chainTag:    chainTag,
		opts:        opts,
		accounts:    opts.Accounts,
		managers:    managers,
		ctx:         ctx,
		cancel:      cancel,
		metricsChan: make(chan blockMetrics, 100),
	}

	go client.pollForBlocks()
	go client.processMetrics()

	return rt.ToValue(client).ToObject(rt)
}

func registerMetrics(vu modules.VU) vechainMetrics {
	registry := vu.InitEnv().Registry
	m := vechainMetrics{
		RequestDuration: registry.MustNewMetric("vechain_req_duration", metrics.Trend, metrics.Time),
		TimeToMine:      registry.MustNewMetric("vechain_time_to_mine", metrics.Trend, metrics.Time),
		Block:           registry.MustNewMetric("vechain_block", metrics.Counter, metrics.Default),
		GasUsed:         registry.MustNewMetric("vechain_gas_used", metrics.Trend, metrics.Default),
		TPS:             registry.MustNewMetric("vechain_tps", metrics.Trend, metrics.Default),
		BlockTime:       registry.MustNewMetric("vechain_block_time", metrics.Trend, metrics.Time),
		BaseFee:         registry.MustNewMetric("vechain_base_fee", metrics.Trend, metrics.Default),
	}

	return m
}

func (c *Client) reportMetricsFromStats(call string, t time.Duration) {
	registry := metrics.NewRegistry()

	if c.vu != nil {
		metrics.PushIfNotDone(c.vu.Context(), c.vu.State().Samples, metrics.Sample{
			TimeSeries: metrics.TimeSeries{
				Metric: c.metrics.RequestDuration,
				Tags:   registry.RootTagSet().With("call", call),
			},
			Value: float64(t / time.Millisecond),
			Time:  time.Now(),
		})
	}
}

// options defines configuration options for the client.
type options struct {
	URL      string `json:"url,omitempty"`
	Mnemonic string `json:"mnemonic,omitempty"`
	Accounts int    `json:"accounts,omitempty"`
}

// newOptionsFrom validates and instantiates an options struct from its map representation
// as obtained by calling a Goja's Runtime.ExportTo.
func newOptionsFrom(argument map[string]interface{}) (*options, error) {
	jsonStr, err := json.Marshal(argument)
	if err != nil {
		return nil, fmt.Errorf("unable to serialize options to JSON %w", err)
	}

	// Instantiate a JSON decoder which will error on unknown
	// fields. As a result, if the input map contains an unknown
	// option, this function will produce an error.
	decoder := json.NewDecoder(bytes.NewReader(jsonStr))
	decoder.DisallowUnknownFields()

	var opts options
	err = decoder.Decode(&opts)
	if err != nil {
		return nil, fmt.Errorf("unable to decode options %w", err)
	}

	return &opts, nil
}
