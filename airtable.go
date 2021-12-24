package main

import (
	"fmt"
	"github.com/brianloveswords/airtable"
	"github.com/plaid/plaid-go/plaid"
	"os"
	_ "time"
)

var _ = fmt.Println

type TransactionFields struct {
	PlaidID      string
	AccountID    string
	Amount       float64
	Name         string
	MerchantName string
	Pending      bool
	DateTime     string
	Category1   string
	Category2   string
	Category3   string
	Category12  airtable.MultiSelect
	Category123 airtable.MultiSelect
}

type TransactionRecord struct {
	airtable.Record
	Fields   TransactionFields
	Typecast bool
}

func Sync(transactions []plaid.Transaction) error {
	client := airtable.Client{
		APIKey: os.Getenv("AIRTABLE_KEY"),
		BaseID: "appe5OMMgxrJhsr2U",
	}

	expenses := client.Table("Credit Card Expenses")

	plaidTransactions := make([]TransactionRecord, len(transactions))
	for i, t := range transactions {
		s := func(tags []string, n int) string {
			if n >= len(tags) {
				return ""
			}
			return tags[n]
		}
		plaidTransactions[i] = TransactionRecord{Fields: TransactionFields{
			PlaidID:      t.ID,
			AccountID:    t.AccountID,
			Amount:       t.Amount,
			Name:         t.Name,
			MerchantName: t.MerchantName,
			Pending:      t.Pending,
			DateTime:     t.Date,
			Category1:    s(t.Category, 0),
			Category2:    s(t.Category, 1),
			Category3:    s(t.Category, 2),
			Category12:   t.Category[:2],
			Category123:  t.Category,
		}, Typecast: true}
	}
	plaidArranged := byAccountIDbyTransactionID(plaidTransactions)
	fmt.Println(plaidArranged)

	var airtableTransactions []TransactionRecord
	err := expenses.List(&airtableTransactions, &airtable.Options{})
	if err != nil {
		return err
	}

	airtableArranged := byAccountIDbyTransactionID(airtableTransactions)
	fmt.Println(airtableArranged)

	for accountID, transactions := range plaidArranged {
		updates := updateAccount(transactions, airtableArranged[accountID])

		for i, t := range updates.ToCreate {
			err := expenses.Create(&t)
			if err != nil {
				return err
			}
			fmt.Printf("Created %d/%d transactions\n", i, len(updates.ToCreate))
		}

		for _, t := range updates.ToUpdate {
			fmt.Println(t)
			err := expenses.Update(&t)
			if err != nil {
				return err
			}
		}

		for _, t := range updates.ToDelete {
			err := expenses.Delete(&t)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func byAccountIDbyTransactionID(ts []TransactionRecord) map[string]map[string]TransactionRecord {
	ret := make(map[string]map[string]TransactionRecord)
	for _, t := range ts {
		byID, ok := ret[t.Fields.AccountID]
		if !ok {
			byID = make(map[string]TransactionRecord)
			ret[t.Fields.AccountID] = byID
		}
		byID[t.Fields.PlaidID] = t
	}
	return ret
}

type AccountUpdate struct {
	ToCreate []TransactionRecord
	ToUpdate []TransactionRecord
	ToDelete []TransactionRecord
}

func updateAccount(plaidTs, airtableTs map[string]TransactionRecord) AccountUpdate {
	var u AccountUpdate
	ids := make(map[string]struct{})
	for id, t := range plaidTs {
		ids[id] = struct{}{}
		existing, ok := airtableTs[id]
		if !ok {
			u.ToCreate = append(u.ToCreate, t)
		} else if false { //existing.Fields != t.Fields {
			_ = existing
			// TODO: make update work
			u.ToUpdate = append(u.ToUpdate, t)
		}
	}

	for id, t := range airtableTs {
		if _, ok := ids[id]; !ok {
			u.ToDelete = append(u.ToDelete, t)
		}
	}
	return u
}
