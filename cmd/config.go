package cmd

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"

	cctx "github.com/cosmos/cosmos-sdk/client/context"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/crypto/keys"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/auth"
	authclient "github.com/cosmos/cosmos-sdk/x/auth/client/utils"
	"github.com/cosmos/go-bip39"
	"github.com/ovrclk/akash/app"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	rpchttp "github.com/tendermint/tendermint/rpc/client/http"
	"gopkg.in/yaml.v2"
)

var (
	akashPrefix = "akash"
	defaultKey  = "default"
	defaultPass = "12345678"
)

// Config represents the application configuration
type Config struct {
	ChainID string `yaml:"chain-id" json:"chain-id"`
	RPCAddr string `yaml:"rpc-addr" json:"rpc-addr"`
	Keyfile string `yaml:"keyfile" json:"keyfile"`
	Keypass string `yaml:"keypass" json:"keypass"`

	gasAdj    float64
	gasPrices sdk.DecCoins

	keybase keys.Keybase
	address sdk.AccAddress
	Amino   *codec.Codec
}

// CLICtx returns the CLICtx object with some defaults set
func (c *Config) CLICtx(client *rpchttp.HTTP) cctx.CLIContext {
	return cctx.CLIContext{
		FromAddress:   c.address,
		Client:        client,
		ChainID:       c.ChainID,
		Keybase:       c.keybase,
		NodeURI:       c.RPCAddr,
		Input:         os.Stdin,
		Output:        os.Stdout,
		OutputFormat:  "json",
		From:          defaultKey,
		BroadcastMode: "sync",
		FromName:      defaultKey,
		Codec:         c.Amino,
		TrustNode:     true,
		UseLedger:     false,
		Simulate:      false,
		GenerateOnly:  false,
		Indent:        true,
		SkipConfirm:   true,
	}
}

// GetAccAddress returns the deployer account address
func (c *Config) GetAccAddress() sdk.AccAddress {
	if c.address != nil {
		return c.address
	}

	// ensure we are returning akash addresses
	sdkConf := sdk.GetConfig()
	sdkConf.SetBech32PrefixForAccount(akashPrefix, akashPrefix+"pub")

	if c.keybase != nil {
		k, _ := c.keybase.Get(defaultKey)
		return k.GetAddress()
	}
	return nil
}

// initConfig reads in config file and ENV variables if set.
func initConfig(cmd *cobra.Command) error {
	home, err := cmd.PersistentFlags().GetString(flags.FlagHome)
	if err != nil {
		return err
	}

	config = &Config{}
	cfgPath = path.Join(home, "config.yaml")
	if _, err := os.Stat(cfgPath); err == nil {
		viper.SetConfigFile(cfgPath)
		if err := viper.ReadInConfig(); err == nil {
			// read the config file bytes
			file, err := ioutil.ReadFile(viper.ConfigFileUsed())
			if err != nil {
				fmt.Println("Error reading file:", err)
				os.Exit(1)
			}

			// unmarshall them into the struct
			err = yaml.Unmarshal(file, config)
			if err != nil {
				fmt.Println("Error unmarshalling config:", err)
				os.Exit(1)
			}

			// ensure config has []*relayer.Chain used for all chain operations
			err = validateConfig(config)
			if err != nil {
				fmt.Println("Error parsing chain config:", err)
				os.Exit(1)
			}
		}
	} else if os.IsNotExist(err) {
		// If the config file doesn't exist, just log and exit
		fmt.Printf("config file %s doesn't exist\n", cfgPath)
		return nil
	}
	return nil
}

// validateConfig validates all the props in the config file
func validateConfig(c *Config) (err error) {
	// Ensure that codecs exist
	c.Amino = app.MakeCodec()

	// If we are unable to create a new RPC client (rpc-addr doesn't parse) return err
	if _, err = rpchttp.New(c.RPCAddr, "/websocket"); err != nil {
		return
	}

	// Warn if priv key specified and not exist at given path
	keypath := path.Join(homePath, c.Keyfile)
	if _, err = os.Stat(keypath); os.IsNotExist(err) {
		fmt.Printf("Private key specified in the config file doesn't exist: %s\n", keypath)
		return nil
	}

	// Warn if keypass isn't set or doesn't unlock the given keyfile?
	if err = c.CreateKeybase(); err != nil {
		return err
	}

	// Set address on the struct
	c.GetAccAddress()

	return
}

// NewTMClient returns a new tendermint RPC client from the config
// NOTE: there shouldn't be errors here because we already check them
// in validateConfig
func (c *Config) NewTMClient() *rpchttp.HTTP {
	out, _ := rpchttp.New(c.RPCAddr, "/websocket")
	return out
}

// CreateKeybase returns the
func (c *Config) CreateKeybase() (err error) {
	kb := keys.NewInMemory()
	kf, err := os.Open(path.Join(homePath, c.Keyfile))
	if err != nil {
		return
	}
	byt, err := ioutil.ReadAll(kf)
	if err != nil {
		return
	}
	err = kb.ImportPrivKey(defaultKey, string(byt), c.Keypass)
	c.keybase = kb
	return
}

// CreateKey creates a new private key
func (c *Config) CreateKey() (err error) {
	kp := path.Join(homePath, c.Keyfile)

	if _, err := os.Stat(kp); !os.IsNotExist(err) {
		return fmt.Errorf("keyfile %s already exists", kp)
	} else {
		fmt.Printf("Creating %s ...\n", kp)
	}

	kb := keys.NewInMemory()

	entropySeed, err := bip39.NewEntropy(256)
	if err != nil {
		return err
	}
	mnemonic, err := bip39.NewMnemonic(entropySeed)
	if err != nil {
		return err
	}

	if _, err = kb.CreateAccount(defaultKey, mnemonic, defaultPass, defaultPass, keys.CreateHDPath(0, 0).String(), keys.Secp256k1); err != nil {
		return err
	}

	armor, err := kb.ExportPrivKey(defaultKey, defaultPass, defaultPass)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(kp, []byte(armor), 0644)
}

// SendMsgs sends given sdk messages
func (c *Config) SendMsgs(datagrams []sdk.Msg) (res sdk.TxResponse, err error) {
	// validate basic all the msgs
	for _, msg := range datagrams {
		if err := msg.ValidateBasic(); err != nil {
			return res, err
		}
	}

	var out []byte
	if out, err = c.BuildAndSignTx(datagrams); err != nil {
		return res, err
	}
	return c.BroadcastTxCommit(out)
}

// BuildAndSignTx takes messages and builds, signs and marshals a sdk.Tx to prepare it for broadcast
func (c *Config) BuildAndSignTx(msgs []sdk.Msg) ([]byte, error) {
	// Fetch account and sequence numbers for the account
	var txBldr auth.TxBuilder
	ctx := c.CLICtx(c.NewTMClient())
	acc, err := auth.NewAccountRetriever(ctx).GetAccount(c.GetAccAddress())
	if err != nil {
		return nil, err
	}

	// Create the transaction builder with some sane defaults
	// TODO: add some debug output?
	txBldr = auth.NewTxBuilder(
		auth.DefaultTxEncoder(c.Amino),
		acc.GetAccountNumber(),
		acc.GetSequence(),
		200000,
		c.gasAdj,
		true,
		c.ChainID,
		"",
		sdk.NewCoins(),
		c.gasPrices,
	).WithKeybase(c.keybase)

	// Estimate the gas
	if txBldr, err = authclient.EnrichWithGas(txBldr, ctx, msgs); err != nil {
		return nil, err
	}

	// Return nil or the signature error
	return txBldr.BuildAndSign(defaultKey, c.Keypass, msgs)
}

// BroadcastTxCommit takes the marshaled transaction bytes and broadcasts them
func (c *Config) BroadcastTxCommit(txBytes []byte) (sdk.TxResponse, error) {
	// TODO: add some debug output?
	return c.CLICtx(c.NewTMClient()).BroadcastTxCommit(txBytes)
}

// BlockHeight returns the current block height from the configured client
func (c *Config) BlockHeight() (uint64, error) {
	status, err := c.NewTMClient().Status()
	if err != nil {
		return 0, err
	}
	return uint64(status.SyncInfo.LatestBlockHeight), nil
}

func writeConfig(cmd *cobra.Command, cfg *Config) (err error) {
	if err = os.MkdirAll(filepath.Dir(cfgPath), os.ModePerm); err != nil {
		return
	}
	// marshal the new config
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	// overwrite the config file
	err = ioutil.WriteFile(cfgPath, out, 0644)
	if err != nil {
		return err
	}

	// reset the global variable
	config = cfg
	return
}
