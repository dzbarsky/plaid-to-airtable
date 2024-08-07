package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/landakram/plaid-cli/pkg/plaid_cli"
	"github.com/manifoldco/promptui"
	"github.com/plaid/plaid-go/v27/plaid"
	"github.com/spf13/cobra"

	"github.com/spf13/viper"
)

type idAndAlias struct{ id, alias string }

func sliceToMap(slice []string) map[string]bool {
	set := make(map[string]bool, len(slice))
	for _, s := range slice {
		set[s] = true
	}
	return set
}

// See https://plaid.com/docs/link/customization/#language-and-country
var plaidSupportedCountries = []string{"US", "CA", "GB", "IE", "ES", "FR", "NL"}
var plaidSupportedLanguages = []string{"en", "fr", "es", "nl"}

func AreValidCountries(countries []string) bool {
	supportedCountries := sliceToMap(plaidSupportedCountries)
	for _, c := range countries {
		if !supportedCountries[c] {
			return false
		}
	}

	return true
}

func IsValidLanguageCode(lang string) bool {
	supportedLanguages := sliceToMap(plaidSupportedLanguages)
	return supportedLanguages[lang]
}

func main() {
	log.SetFlags(0)

	usr, _ := user.Current()
	dir := usr.HomeDir
	viper.SetDefault("cli.data_dir", filepath.Join(dir, ".plaid-cli"))

	dataDir := viper.GetString("cli.data_dir")
	data, err := plaid_cli.LoadData(dataDir)

	if err != nil {
		log.Fatal(err)
	}

	viper.SetConfigName("config")
	viper.SetConfigType("toml")
	viper.AddConfigPath(dataDir)
	viper.AddConfigPath(".")
	err = viper.ReadInConfig()
	if err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// Config file not found; ignore error if desired
		} else {
			log.Fatal(err)
		}
	}

	viper.SetEnvPrefix("")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	viper.AutomaticEnv()

	cfg := plaid.NewConfiguration()
	cfg.AddDefaultHeader("PLAID-CLIENT-ID", viper.GetString("plaid.client_id"))
	cfg.AddDefaultHeader("PLAID-SECRET", viper.GetString("plaid.secret"))
	cfg.UseEnvironment(plaid.Production)
	client := plaid.NewAPIClient(cfg)

	ctx := context.Background()
	linker := plaid_cli.NewLinker(data, client, []plaid.CountryCode{"US"}, "en")

	linkCommand := &cobra.Command{
		Use:   "link [ITEM-ID-OR-ALIAS]",
		Short: "Link an institution so plaid-cli can pull transactions",
		Long:  "Link an institution so plaid-cli can pull transactions. An item ID or alias can be passed to initiate a relink.",
		Args:  cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			port := viper.GetString("link.port")

			var tokenPair *plaid_cli.TokenPair

			var err error

			if len(args) > 0 && len(args[0]) > 0 {
				itemOrAlias := args[0]

				itemID, ok := data.Aliases[itemOrAlias]
				if ok {
					itemOrAlias = itemID
				}

				err = linker.Relink(ctx, itemOrAlias, port)
				if err != nil {
					log.Fatalln("Cannot relink", err)
				}
				log.Println("Institution relinked!")
				return
			} else {
				tokenPair, err = linker.Link(ctx, port)
				if err != nil {
					log.Fatalln("Cannot link", err)
				}
				data.Tokens[tokenPair.ItemID] = tokenPair.AccessToken
				err = data.Save()
			}

			if err != nil {
				log.Fatalln("Cannot save", err)
			}

			log.Println("Institution linked!")
			log.Println(fmt.Sprintf("Item ID: %s", tokenPair.ItemID))

			if alias, ok := data.BackAliases[tokenPair.ItemID]; ok {
				log.Println(fmt.Sprintf("Alias: %s", alias))
				return
			}

			validate := func(input string) error {
				matched, err := regexp.Match(`^\w+$`, []byte(input))
				if err != nil {
					return err
				}

				if !matched && input != "" {
					return errors.New("Valid characters: [0-9A-Za-z_]")
				}

				return nil
			}

			log.Println("You can give the institution a friendly alias and use that instead of the item ID in most commands.")
			prompt := promptui.Prompt{
				Label:    "Alias (default: none)",
				Validate: validate,
			}

			input, err := prompt.Run()
			if err != nil {
				log.Fatalln(err)
			}

			if input != "" {
				err = SetAlias(data, tokenPair.ItemID, input)
				if err != nil {
					log.Fatalln(err)
				}
			}
		},
	}

	linkCommand.Flags().StringP("port", "p", "9090", "Port on which to serve Plaid Link")
	viper.BindPFlag("link.port", linkCommand.Flags().Lookup("port"))

	tokensCommand := &cobra.Command{
		Use:   "tokens",
		Short: "List access tokens",
		Run: func(cmd *cobra.Command, args []string) {
			resolved := make(map[string]string)
			for itemID, token := range data.Tokens {
				if alias, ok := data.BackAliases[itemID]; ok {
					resolved[alias] = token
				} else {
					resolved[itemID] = token
				}
			}

			printJSON, err := json.MarshalIndent(resolved, "", "  ")
			if err != nil {
				log.Fatalln(err)
			}
			fmt.Println(string(printJSON))
		},
	}

	aliasCommand := &cobra.Command{
		Use:   "alias [ITEM-ID] [NAME]",
		Short: "Give a linked institution a friendly name",
		Long:  "Give a linked institution a friendly name. You can use this name instead of the idem ID in most commands.",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			itemID := args[0]
			alias := args[1]

			err := SetAlias(data, itemID, alias)
			if err != nil {
				log.Fatalln(err)
			}
		},
	}

	aliasesCommand := &cobra.Command{
		Use:   "aliases",
		Short: "List aliases",
		Run: func(cmd *cobra.Command, args []string) {
			printJSON, err := json.MarshalIndent(data.Aliases, "", "  ")
			if err != nil {
				log.Fatalln(err)
			}
			fmt.Println(string(printJSON))
		},
	}

	accountsCommand := &cobra.Command{
		Use:   "accounts [ITEM-ID-OR-ALIAS]",
		Short: "List accounts for a given institution",
		Long:  "List accounts for a given institution. An account ID returned from this command can be used as a filter when listing transactions.",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			itemOrAlias := args[0]

			var items []idAndAlias

			if itemOrAlias == "all" {
				for alias, itemID := range data.Aliases {
					items = append(items, idAndAlias{itemID, alias})
				}
			} else {
				itemID, ok := data.Aliases[itemOrAlias]
				if !ok {
					panic("Unknown alias")
				}
				items = append(items, idAndAlias{itemID, itemOrAlias})
			}

			for _, item := range items {
				if item.id == "7jKq173RmNfQyGvRnw6XFxQjKVlo8DcgjdEMJ" {
					// Sandbox item
					continue
				}
				err = WithRelinkOnAuthError(ctx, idAndAlias{id: item.id}, data, linker, func() error {
					fmt.Println("Syncing accounts for ", item)
					token := data.Tokens[item.id]
					res, _, err := client.PlaidApi.AccountsGet(ctx).AccountsGetRequest(plaid.AccountsGetRequest{
						AccessToken: token,
					}).Execute()
					if err != nil {
						log.Println(item, err)
						return nil
					}

					err = SyncAccounts(res.Accounts)
					if err != nil {
						return err
					}

					b, err := json.MarshalIndent(res.Accounts, "", "  ")
					if err != nil {
						return err
					}

					fmt.Println(string(b))

					return nil
				})
				if err != nil {
					log.Fatalln(err)
				}
			}

		},
	}

	var fromFlag string
	var toFlag string
	var accountID string
	var outputFormat string
	transactionsCommand := &cobra.Command{
		Use:   "transactions [ITEM-ID-OR-ALIAS]",
		Short: "List transactions for a given institution",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			itemOrAlias := args[0]
			itemID, ok := data.Aliases[itemOrAlias]
			if ok {
				itemOrAlias = itemID
			}

			err := WithRelinkOnAuthError(ctx, idAndAlias{id: itemOrAlias}, data, linker, func() error {
				token := data.Tokens[itemOrAlias]

				var accountIDs []string
				if len(accountID) > 0 {
					accountIDs = append(accountIDs, accountID)
				}

				options := plaid.NewTransactionsGetRequestOptions()
				options.SetAccountIds(accountIDs)
				req := plaid.TransactionsGetRequest{
					StartDate:   fromFlag,
					EndDate:     toFlag,
					Options:     options,
					AccessToken: token,
				}

				transactions, err := AllTransactions(ctx, req, client)
				if err != nil {
					return err
				}

				serializer, err := NewTransactionSerializer(outputFormat)
				if err != nil {
					return err
				}

				b, err := serializer.serialize(transactions)
				if err != nil {
					return err
				}

				fmt.Println(string(b))

				return nil
			})

			if err != nil {
				log.Fatalln(err)
			}
		},
	}
	transactionsCommand.Flags().StringVarP(&fromFlag, "from", "f", "", "Date of first transaction (required)")
	transactionsCommand.MarkFlagRequired("from")

	transactionsCommand.Flags().StringVarP(&toFlag, "to", "t", "", "Date of last transaction (required)")
	transactionsCommand.MarkFlagRequired("to")

	transactionsCommand.Flags().StringVarP(&outputFormat, "output-format", "o", "json", "Output format")
	transactionsCommand.Flags().StringVarP(&accountID, "account-id", "a", "", "Fetch transactions for this account ID only.")

	airtableSyncCommand := &cobra.Command{
		Use:   "sync-transactions [ITEM-ID-OR-ALIAS]",
		Short: "Sync transactions for a given institution",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			itemOrAlias := args[0]

			var items []idAndAlias

			if itemOrAlias == "all" {
				for alias, itemID := range data.Aliases {
					items = append(items, idAndAlias{itemID, alias})
				}
			} else {
				itemID, ok := data.Aliases[itemOrAlias]
				if !ok {
					panic("Unknown alias")
				}
				items = append(items, idAndAlias{itemID, itemOrAlias})
			}

			var transactionsMu sync.Mutex
			var allTransactions []plaid.Transaction

			var wg sync.WaitGroup

			for _, item := range items {
				if item.id == "7jKq173RmNfQyGvRnw6XFxQjKVlo8DcgjdEMJ" {
					// Sandbox item
					continue
				}
				wg.Add(1)
				go func(item idAndAlias) {
					defer wg.Done()
					fmt.Println("Downloading transactions for ", item)
					err := WithRelinkOnAuthError(ctx, item, data, linker, func() error {
						token := data.Tokens[item.id]

						var accountIDs []string
						if len(accountID) > 0 {
							accountIDs = append(accountIDs, accountID)
						}

						layout := "2006-01-02"
						now := time.Now()
						start := time.Date(2024, time.May, 24, 0, 0, 0, 0, time.Local)
						if item.alias == "citi" {
							start = time.Date(2023, time.August, 1, 0, 0, 0, 0, time.Local)
						}

						options := plaid.NewTransactionsGetRequestOptions()
						options.SetAccountIds(accountIDs)
						req := plaid.TransactionsGetRequest{
							StartDate:   start.Format(layout),
							EndDate:     now.Format(layout),
							Options:     options,
							AccessToken: token,
						}

						transactions, err := AllTransactions(ctx, req, client)
						if err != nil {
							return err
						}

						/*data, err := json.Marshal(transactions)
						if err != nil {
							return err
						}
						err = os.WriteFile("/tmp/citi.json", data, 0777)
						if err != nil {
							return err
						}*/

						transactionsMu.Lock()
						allTransactions = append(allTransactions, transactions...)
						transactionsMu.Unlock()
						return nil
					})

					if err != nil {
						log.Println(item, err)
					}
				}(item)
			}

			airtableTransactions, err := FetchAirtableTransactions()
			if err != nil {
				log.Fatalln(err)
			}

			wg.Wait()

			fmt.Println("Syncing all transactions")
			err = Sync(allTransactions, airtableTransactions)
			if err != nil {
				log.Fatalln(err)
			}
		},
	}

	unlinkCommand := &cobra.Command{
		Use:   "unlink [ITEM-ID-OR-ALIAS]",
		Short: "Unlink given institution",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			itemOrAlias := args[0]

			var items []idAndAlias

			if itemOrAlias == "all" {
				for alias, itemID := range data.Aliases {
					items = append(items, idAndAlias{itemID, alias})
				}
			} else {
				itemID, ok := data.Aliases[itemOrAlias]
				if !ok {
					panic("Unknown alias")
				}
				items = append(items, idAndAlias{itemID, itemOrAlias})
			}

			for _, item := range items {
				_, _, err := client.PlaidApi.ItemRemove(ctx).ItemRemoveRequest(plaid.ItemRemoveRequest{
					AccessToken: data.Tokens[item.id],
				}).Execute()

				if err != nil {
					log.Fatal("Could not unlink", item, err)
				}

				delete(data.Aliases, item.alias)
				delete(data.BackAliases, item.id)
				delete(data.Tokens, item.id)
				err = data.Save()
				if err != nil {
					log.Println(item, err)
				}
			}
		},
	}

	airtableFixCommand := &cobra.Command{
		Use:   "fix-airtable",
		Short: "Fix duplicate airtable transactions",
		Run: func(cmd *cobra.Command, args []string) {
			airtableTransactions, err := FetchAirtableTransactions()
			if err != nil {
				log.Fatalln(err)
			}

			fmt.Println("Syncing all transactions")
			err = FixAT(airtableTransactions)
			if err != nil {
				log.Fatalln(err)
			}
		},
	}

	var withStatusFlag bool
	var withOptionalMetadataFlag bool
	insitutionCommand := &cobra.Command{
		Use:   "institution [ITEM-ID-OR-ALIAS]",
		Short: "Get information about an institution",
		Long:  "Get information about an institution. Status can be reported using a flag.",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			itemOrAlias := args[0]
			itemID, ok := data.Aliases[itemOrAlias]
			if ok {
				itemOrAlias = itemID
			}

			err := WithRelinkOnAuthError(ctx, idAndAlias{id: itemOrAlias}, data, linker, func() error {
				token := data.Tokens[itemOrAlias]

				_, _, err := client.PlaidApi.ItemGet(ctx).ItemGetRequest(plaid.ItemGetRequest{
					AccessToken: token,
				}).Execute()
				if err != nil {
					return err
				}

				/*instID := itemResp.Item.InstitutionId

				opts := plaid.GetInstitutionByIDOptions{
					IncludeOptionalMetadata: withOptionalMetadataFlag,
					IncludeStatus:           withStatusFlag,
				}
				resp, err := client.GetInstitutionByIDWithOptions(instID, countries, opts)
				if err != nil {
					return err
				}

				b, err := json.MarshalIndent(resp.Institution, "", "  ")
				if err != nil {
					return err
				}

				fmt.Println(string(b))*/

				return nil
			})

			if err != nil {
				log.Fatalln(err)
			}
		},
	}
	insitutionCommand.Flags().BoolVarP(&withStatusFlag, "status", "s", false, "Fetch institution status")
	insitutionCommand.Flags().BoolVarP(&withOptionalMetadataFlag, "optional-metadata", "m", false, "Fetch optional metadata like logo and URL")

	rootCommand := &cobra.Command{
		Use:   "plaid-cli",
		Short: "Link bank accounts and get transactions from the command line.",
		Long: `plaid-cli 🤑

plaid-cli is a CLI tool for working with the Plaid API.

You can use plaid-cli to link bank accounts and pull transactions in multiple 
output formats from the comfort of the command line.

Configuration:
  To get started, you'll need Plaid API credentials, which you can get by visiting
  https://dashboard.plaid.com/team/keys after signing up for free.
  
  plaid-cli will look at the following environment variables for API credentials:
  
    PLAID_CLIENT_ID=<client id>
    PLAID_SECRET=<devlopment secret>
    PLAID_ENVIRONMENT=development
    PLAID_LANGUAGE=en  # optional, detected using system's locale
    PLAID_COUNTRIES=US # optional, detected using system's locale
  
  I recommend setting and exporting these on shell startup.
  
  API credentials can also be specified using a config file located at 
  ~/.plaid-cli/config.toml:
  
    [plaid]
    client_id = "<client id>"
    secret = "<development secret>"
    environment = "development"
  
  After setting those API credentials, plaid-cli is ready to use! 
  You'll probably want to run 'plaid-cli link' next.
  
  Please see the README (https://github.com/landakram/plaid-cli/blob/master/README.md) 
  for more detailed usage instructions.

  Made by @landakram.
`,
	}
	rootCommand.AddCommand(linkCommand)
	rootCommand.AddCommand(tokensCommand)
	rootCommand.AddCommand(aliasCommand)
	rootCommand.AddCommand(aliasesCommand)
	rootCommand.AddCommand(accountsCommand)
	rootCommand.AddCommand(transactionsCommand)
	rootCommand.AddCommand(airtableSyncCommand)
	rootCommand.AddCommand(airtableFixCommand)
	rootCommand.AddCommand(insitutionCommand)
	rootCommand.AddCommand(unlinkCommand)

	if !viper.IsSet("plaid.client_id") {
		log.Println("⚠️  PLAID_CLIENT_ID not set. Please see the configuration instructions below.")
		rootCommand.Help()
		os.Exit(1)
	}
	if !viper.IsSet("plaid.secret") {
		log.Println("⚠️ PLAID_SECRET not set. Please see the configuration instructions below.")
		rootCommand.Help()
		os.Exit(1)
	}

	rootCommand.Execute()
}

func AllTransactions(ctx context.Context, req plaid.TransactionsGetRequest, client *plaid.APIClient) ([]plaid.Transaction, error) {
	var transactions []plaid.Transaction

	res, _, err := client.PlaidApi.TransactionsGet(ctx).TransactionsGetRequest(req).Execute()
	if err != nil {
		return transactions, err
	}

	transactions = append(transactions, res.Transactions...)

	for len(transactions) < int(res.TotalTransactions) {
		req.Options.SetOffset(*req.Options.Offset + *req.Options.Count)
		res, _, err := client.PlaidApi.TransactionsGet(ctx).TransactionsGetRequest(req).Execute()
		if err != nil {
			return transactions, err
		}

		transactions = append(transactions, res.Transactions...)

	}

	return transactions, nil
}

func WithRelinkOnAuthError(ctx context.Context, item idAndAlias, data *plaid_cli.Data, linker *plaid_cli.Linker, action func() error) error {
	err := action()
	e, _ := plaid.ToPlaidError(err)
	if e.ErrorCode == "ITEM_LOGIN_REQUIRED" {
		log.Printf("Login expired for %s (%s). Relinking...\n", item.alias, item.id)

		port := viper.GetString("link.port")

		err = linker.Relink(ctx, item.id, port)

		if err != nil {
			return err
		}

		log.Println("Re-running action...")

		err = action()
	}

	return err
}

type TransactionSerializer interface {
	serialize(txs []plaid.Transaction) ([]byte, error)
}

func NewTransactionSerializer(t string) (TransactionSerializer, error) {
	switch t {
	case "csv":
		return &CSVSerializer{}, nil
	case "json":
		return &JSONSerializer{}, nil
	default:
		return nil, errors.New(fmt.Sprintf("Invalid output format: %s", t))
	}
}

type CSVSerializer struct{}

func (w *CSVSerializer) serialize(txs []plaid.Transaction) ([]byte, error) {
	var records [][]string
	for _, tx := range txs {
		sanitizedName := strings.ReplaceAll(tx.Name, ",", "")
		records = append(records, []string{tx.Date, fmt.Sprintf("%f", tx.Amount), sanitizedName})
	}

	b := bytes.NewBufferString("")
	writer := csv.NewWriter(b)
	err := writer.Write([]string{"Date", "Amount", "Description"})
	if err != nil {
		return nil, err
	}
	err = writer.WriteAll(records)
	if err != nil {
		return nil, err
	}

	return b.Bytes(), err
}

func SetAlias(data *plaid_cli.Data, itemID string, alias string) error {
	if _, ok := data.Tokens[itemID]; !ok {
		return errors.New(fmt.Sprintf("No access token found for item ID `%s`. Try re-linking your account with `plaid-cli link`.", itemID))
	}

	data.Aliases[alias] = itemID
	data.BackAliases[itemID] = alias
	err := data.Save()
	if err != nil {
		return err
	}

	log.Println(fmt.Sprintf("Aliased %s to %s.", itemID, alias))

	return nil
}

type JSONSerializer struct{}

func (w *JSONSerializer) serialize(txs []plaid.Transaction) ([]byte, error) {
	return json.MarshalIndent(txs, "", "  ")
}
