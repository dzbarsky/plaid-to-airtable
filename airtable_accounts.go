package main

import (
	"fmt"
	"github.com/brianloveswords/airtable"
	"github.com/plaid/plaid-go/plaid"
	"os"
)

type AccountFields struct {
	AccountID    string
	Name         string
}

type AccountRecord struct {
	airtable.Record
	Fields   AccountFields
}

func SyncAccounts(accounts []plaid.Account) error {
	client := airtable.Client{
		APIKey: os.Getenv("AIRTABLE_KEY"),
		BaseID: "appxCfKnRz94NZadj",
	}

	accountsTable := client.Table("Accounts")

	plaidAccounts := make([]AccountRecord, len(accounts))
	for i, a := range accounts {
		name := a.OfficialName
		if name == "" {
			name = a.Name
		}
		plaidAccounts[i] = AccountRecord{Fields: AccountFields{
			AccountID:    a.AccountID,
			Name:         name,
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

