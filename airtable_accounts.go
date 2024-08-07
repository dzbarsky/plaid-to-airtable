package main

import (
	"fmt"
	"os"

	"github.com/brianloveswords/airtable"
	"github.com/plaid/plaid-go/v27/plaid"
)

type AccountFields struct {
	AccountID string
	Name      string
	Mask      string
}

type AccountRecord struct {
	airtable.Record
	Fields AccountFields
}

func SyncAccounts(accounts []plaid.AccountBase) error {
	client := airtable.Client{
		APIKey: os.Getenv("AIRTABLE_KEY"),
		BaseID: "appxCfKnRz94NZadj",
	}

	accountsTable := client.Table("Accounts")

	plaidAccounts := make([]AccountRecord, len(accounts))
	for i, a := range accounts {
		name := val(a.OfficialName)
		if name == "" {
			name = a.Name
		}
		plaidAccounts[i] = AccountRecord{Fields: AccountFields{
			AccountID: a.AccountId,
			Name:      name,
			Mask:      val(a.Mask),
		}}
	}

	var airtableAccounts []AccountRecord
	err := accountsTable.List(&airtableAccounts, &airtable.Options{})
	if err != nil {
		return err
	}
	existingIDs := map[string]struct{}{}
	for _, account := range airtableAccounts {
		existingIDs[account.Fields.AccountID] = struct{}{}
	}

	for i, account := range plaidAccounts {
		if _, ok := existingIDs[account.Fields.AccountID]; ok {
			continue
		}

		err := accountsTable.Create(&account)
		if err != nil {
			return err
		}
		fmt.Printf("Created %d/%d account\n", i, len(plaidAccounts))
	}

	return nil
}
