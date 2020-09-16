package app

import (
	"encoding/json"
	"io"
	"os"

	abci "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/libs/log"
	tmos "github.com/tendermint/tendermint/libs/os"
	dbm "github.com/tendermint/tm-db"

	bam "github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/simapp"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/cosmos/cosmos-sdk/version"
	"github.com/cosmos/cosmos-sdk/x/auth"

	// "github.com/cosmos/cosmos-sdk/x/auth/ante"
	"github.com/cosmos/cosmos-sdk/x/bank"
	"github.com/cosmos/cosmos-sdk/x/genutil"
	"github.com/cosmos/cosmos-sdk/x/params"
	"github.com/cosmos/cosmos-sdk/x/staking"
	"github.com/cosmos/cosmos-sdk/x/supply"
	"github.com/tosch110/ethapp/x/ethapp"
	ethappkeeper "github.com/tosch110/ethapp/x/ethapp/keeper"
	ethapptypes "github.com/tosch110/ethapp/x/ethapp/types"

	// These lines are Ethermint additions

	"github.com/cosmos/ethermint/app/ante"
	emintcrypto "github.com/cosmos/ethermint/crypto"
	ethermint "github.com/cosmos/ethermint/types"
	"github.com/cosmos/ethermint/x/evm"
	// this line is used by starport scaffolding
)

const appName = "ethapp"

var (
	DefaultCLIHome  = os.ExpandEnv("$HOME/.ethappcli")
	DefaultNodeHome = os.ExpandEnv("$HOME/.ethappd")
	ModuleBasics    = module.NewBasicManager(
		genutil.AppModuleBasic{},
		auth.AppModuleBasic{},
		bank.AppModuleBasic{},
		staking.AppModuleBasic{},
		params.AppModuleBasic{},
		supply.AppModuleBasic{},
		ethapp.AppModuleBasic{},
		// this line is for Ethermint
		evm.AppModuleBasic{},
		// this line is used by starport scaffolding # 2
	)

	maccPerms = map[string][]string{
		auth.FeeCollectorName:     nil,
		staking.BondedPoolName:    {supply.Burner, supply.Staking},
		staking.NotBondedPoolName: {supply.Burner, supply.Staking},
	}
)

func MakeCodec() *codec.Codec {
	var cdc = codec.New()

	ModuleBasics.RegisterCodec(cdc)
	sdk.RegisterCodec(cdc)
	codec.RegisterCrypto(cdc)
	emintcrypto.RegisterCodec(cdc)
	ethermint.RegisterCodec(cdc)

	return cdc.Seal()
}

type NewApp struct {
	*bam.BaseApp
	cdc *codec.Codec

	invCheckPeriod uint

	keys  map[string]*sdk.KVStoreKey
	tKeys map[string]*sdk.TransientStoreKey

	subspaces map[string]params.Subspace

	accountKeeper auth.AccountKeeper
	bankKeeper    bank.Keeper
	stakingKeeper staking.Keeper
	supplyKeeper  supply.Keeper
	paramsKeeper  params.Keeper
	ethappKeeper  ethappkeeper.Keeper
	// this line is for Ethermint

	EvmKeeper evm.Keeper
	// this line is used by starport scaffolding # 3
	mm *module.Manager

	sm *module.SimulationManager
}

var _ simapp.App = (*NewApp)(nil)

func NewInitApp(
	logger log.Logger,
	db dbm.DB,
	traceStore io.Writer,
	loadLatest bool,
	invCheckPeriod uint,
	baseAppOptions ...func(*bam.BaseApp),
) *NewApp {
	cdc := MakeCodec()

	// the TxDecoder has to be changed to evm txdecoder
	// NOTE we use custom Ethermint transaction decoder that supports the sdk.Tx interface instead of sdk.StdTx
	bApp := bam.NewBaseApp(appName, logger, db, evm.TxDecoder(cdc), baseAppOptions...)
	bApp.SetCommitMultiStoreTracer(traceStore)
	bApp.SetAppVersion(version.Version)

	keys := sdk.NewKVStoreKeys(
		bam.MainStoreKey,
		auth.StoreKey,
		staking.StoreKey,
		supply.StoreKey,
		params.StoreKey,
		ethapptypes.StoreKey,
		// this line is used for the Ethermint
		evm.StoreKey,
		// this line is used by starport scaffolding # 5
	)

	tKeys := sdk.NewTransientStoreKeys(staking.TStoreKey, params.TStoreKey)

	var app = &NewApp{
		BaseApp:        bApp,
		cdc:            cdc,
		invCheckPeriod: invCheckPeriod,
		keys:           keys,
		tKeys:          tKeys,
		subspaces:      make(map[string]params.Subspace),
	}

	// init params keeper and subspaces
	app.paramsKeeper = params.NewKeeper(app.cdc, keys[params.StoreKey], tKeys[params.TStoreKey])
	app.subspaces[auth.ModuleName] = app.paramsKeeper.Subspace(auth.DefaultParamspace)
	app.subspaces[bank.ModuleName] = app.paramsKeeper.Subspace(bank.DefaultParamspace)
	app.subspaces[staking.ModuleName] = app.paramsKeeper.Subspace(staking.DefaultParamspace)
	// This line is used for Ethermint
	app.subspaces[evm.ModuleName] = app.paramsKeeper.Subspace(evm.DefaultParamspace)

	app.accountKeeper = auth.NewAccountKeeper(
		cdc,
		keys[auth.StoreKey],
		app.subspaces[auth.ModuleName],
		ethermint.ProtoAccount,
	)

	app.bankKeeper = bank.NewBaseKeeper(
		app.accountKeeper,
		app.subspaces[bank.ModuleName],
		app.ModuleAccountAddrs(),
	)

	app.supplyKeeper = supply.NewKeeper(
		cdc,
		keys[supply.StoreKey],
		app.accountKeeper,
		app.bankKeeper,
		maccPerms,
	)

	stakingKeeper := staking.NewKeeper(
		cdc,
		keys[staking.StoreKey],
		app.supplyKeeper,
		app.subspaces[staking.ModuleName],
	)

	app.stakingKeeper = *stakingKeeper.SetHooks(
		staking.NewMultiStakingHooks(),
	)

	app.ethappKeeper = ethappkeeper.NewKeeper(
		app.bankKeeper,
		app.cdc,
		keys[ethapptypes.StoreKey],
	)

	// this line is used for Ethermint
	app.EvmKeeper = evm.NewKeeper(
		app.cdc, keys[evm.StoreKey], app.subspaces[evm.ModuleName], app.accountKeeper,
	)

	// this line is used by starport scaffolding # 4

	app.mm = module.NewManager(
		genutil.NewAppModule(app.accountKeeper, app.stakingKeeper, app.BaseApp.DeliverTx),
		auth.NewAppModule(app.accountKeeper),
		bank.NewAppModule(app.bankKeeper, app.accountKeeper),
		supply.NewAppModule(app.supplyKeeper, app.accountKeeper),
		ethapp.NewAppModule(app.ethappKeeper, app.bankKeeper),
		staking.NewAppModule(app.stakingKeeper, app.accountKeeper, app.supplyKeeper),
		// this line is used by Ethermint
		evm.NewAppModule(app.EvmKeeper, app.accountKeeper),
		// this line is used by starport scaffolding # 6
	)

	// Next line is changed by Ethermint
	app.mm.SetOrderEndBlockers(staking.ModuleName, evm.ModuleName)

	app.mm.SetOrderInitGenesis(
		staking.ModuleName,
		auth.ModuleName,
		bank.ModuleName,
		ethapptypes.ModuleName,
		supply.ModuleName,
		genutil.ModuleName,
		// this line is used for Ethermint
		evm.ModuleName,
		// this line is used by starport scaffolding # 7
	)

	app.mm.RegisterRoutes(app.Router(), app.QueryRouter())

	app.SetInitChainer(app.InitChainer)
	app.SetBeginBlocker(app.BeginBlocker)
	app.SetEndBlocker(app.EndBlocker)

	// // initialize stores
	// app.MountKVStores(keys)
	// app.MountTransientStores(tKeys)

	app.SetAnteHandler(
		ante.NewAnteHandler(
			app.accountKeeper,
			app.EvmKeeper,
			app.supplyKeeper,
		),
	)

	app.MountKVStores(keys)
	app.MountTransientStores(tKeys)

	if loadLatest {
		err := app.LoadLatestVersion(app.keys[bam.MainStoreKey])
		if err != nil {
			tmos.Exit(err.Error())
		}
	}

	return app
}

type GenesisState map[string]json.RawMessage

func NewDefaultGenesisState() GenesisState {
	return ModuleBasics.DefaultGenesis()
}

func (app *NewApp) InitChainer(ctx sdk.Context, req abci.RequestInitChain) abci.ResponseInitChain {
	var genesisState simapp.GenesisState

	app.cdc.MustUnmarshalJSON(req.AppStateBytes, &genesisState)

	return app.mm.InitGenesis(ctx, genesisState)
}

func (app *NewApp) BeginBlocker(ctx sdk.Context, req abci.RequestBeginBlock) abci.ResponseBeginBlock {
	return app.mm.BeginBlock(ctx, req)
}

func (app *NewApp) EndBlocker(ctx sdk.Context, req abci.RequestEndBlock) abci.ResponseEndBlock {
	return app.mm.EndBlock(ctx, req)
}

func (app *NewApp) LoadHeight(height int64) error {
	return app.LoadVersion(height, app.keys[bam.MainStoreKey])
}

func (app *NewApp) ModuleAccountAddrs() map[string]bool {
	modAccAddrs := make(map[string]bool)
	for acc := range maccPerms {
		modAccAddrs[supply.NewModuleAddress(acc).String()] = true
	}

	return modAccAddrs
}

func (app *NewApp) Codec() *codec.Codec {
	return app.cdc
}

func (app *NewApp) SimulationManager() *module.SimulationManager {
	return app.sm
}

func GetMaccPerms() map[string][]string {
	modAccPerms := make(map[string][]string)
	for k, v := range maccPerms {
		modAccPerms[k] = v
	}
	return modAccPerms
}
