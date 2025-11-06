Endpoints: https://broker-api.sandbox.alpaca.markets

Create an Account

One of the first things you would need to do using Broker API is to create an account for your end user. Depending on the type of setup you have with Alpaca (Fully-Disclosed, Non-Disclosed, Omnibus or RIA) the requirements might differ.

Below is a sample request to create an account for a Fully-Disclosed setup:

/v1/aaccount

request body
```json
{
  "contact": {
    "email_address": "gracious_newton_03124687@example.com",
    "phone_number": "897-555-6158",
    "street_address": [
      "20 N San Mateo Dr"
    ],
    "city": "San Mateo",
    "state": "CA",
    "postal_code": "94401"
  },
  "identity": {
    "given_name": "Gracious",
    "family_name": "Newton",
    "date_of_birth": "1970-01-01",
    "country_of_citizenship": "USA",
    "country_of_birth": "USA",
    "party_type": "",
    "tax_id": "444-55-4321",
    "tax_id_type": "USA_SSN",
    "country_of_tax_residence": "USA",
    "funding_source": [
      "employment_income"
    ]
  },
  "disclosures": {
    "is_control_person": false,
    "is_affiliated_exchange_or_finra": false,
    "is_affiliated_exchange_or_iiroc": false,
    "is_politically_exposed": false,
    "immediate_family_exposed": false,
    "is_discretionary": null
  },
  "agreements": [
    {
      "agreement": "customer_agreement",
      "signed_at": "2025-10-27T02:11:21.325736456Z",
      "ip_address": "127.0.0.1"
    }
  ],
  "documents": [
    {
      "document_type": "identity_verification",
      "document_sub_type": "passport",
      "content": "/9j/4AAQSkZJRgABAQEAYABgAAD/2wBDAAgGBgcGBQgHBwcJCQgKDBQNDAsLDBkSEw8UHRofHh0aHBwgJC4nICIsIxwcKDcpLDAxNDQ0Hyc5PTgyPC4zNDL/2wBDAQkJCQwLDBgNDRgyIRwhMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjL/wAARCAABAAEDASIAAhEBAxEB/8QAHwAAAQUBAQEBAQEAAAAAAAAAAAECAwQFBgcICQoL/8QAtRAAAgEDAwIEAwUFBAQAAAF9AQIDAAQRBRIhMUEGE1FhByJxFDKBkaEII0KxwRVS0fAkM2JyggkKFhcYGRolJicoKSo0NTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqDhIWGh4iJipKTlJWWl5iZmqKjpKWmp6ipqrKztLW2t7i5usLDxMXGx8jJytLT1NXW19jZ2uHi4+Tl5ufo6erx8vP09fb3+Pn6/8QAHwEAAwEBAQEBAQEBAQAAAAAAAAECAwQFBgcICQoL/8QAtREAAgECBAQDBAcFBAQAAQJ3AAECAxEEBSExBhJBUQdhcRMiMoEIFEKRobHBCSMzUvAVYnLRChYkNOEl8RcYGRomJygpKjU2Nzg5OkNERUZHSElKU1RVVldYWVpjZGVmZ2hpanN0dXZ3eHl6goOEhYaHiImKkpOUlZaXmJmaoqOkpaanqKmqsrO0tba3uLm6wsPExcbHyMnK0tPU1dbX2Nna4uPk5ebn6Onq8vP09fb3+Pn6/9oADAMBAAIRAxEAPwD3+iiigD//2Q==",
      "content_data": null,
      "mime_type": "image/jpeg"
    }
  ],
  "trusted_contact": {
    "given_name": "Jane",
    "family_name": "Doe",
    "email_address": "gracious_newton_03124687@example.com"
  },
  "minor_identity": null,
  "entity_id": null,
  "additional_information": "",
  "account_type": "",
  "account_sub_type": null,
  "trading_type": null,
  "auto_approve": null,
  "beneficiaries": null,
  "trading_configurations": null,
  "currency": null,
  "enabled_assets": null,
  "authorized_individuals": null,
  "ultimate_beneficial_owners": null,
  "sub_correspondent": null,
  "primary_account_holder_id": null
}
```

response
```json
{
	"id": "5a78d28e-3b92-4d57-8ed1-2865d23c6df9",
	"account_number": "976908895",
	"status": "SUBMITTED",
	"crypto_status": "INACTIVE",
	"currency": "USD",
	"last_equity": "0",
	"created_at": "2025-10-27T02:44:41.857511Z",
	"contact": {
		"email_address": "gracious_newton_03124687@example.com",
		"phone_number": "897-555-6158",
		"street_address": [
			"20 N San Mateo Dr"
		],
		"local_street_address": null,
		"city": "San Mateo",
		"state": "CA",
		"postal_code": "94401",
		"country": "USA"
	},
	"identity": {
		"given_name": "Gracious",
		"family_name": "Newton",
		"date_of_birth": "1970-01-01",
		"country_of_citizenship": "USA",
		"country_of_birth": "USA",
		"party_type": "natural_person",
		"tax_id_type": "USA_SSN",
		"country_of_tax_residence": "USA",
		"funding_source": [
			"employment_income"
		]
	},
	"disclosures": {
		"is_control_person": false,
		"is_affiliated_exchange_or_finra": false,
		"is_affiliated_exchange_or_iiroc": false,
		"is_politically_exposed": false,
		"immediate_family_exposed": false,
		"is_discretionary": false
	},
	"agreements": [
		{
			"agreement": "customer_agreement",
			"signed_at": "2025-10-27T02:11:21.325736456Z",
			"ip_address": "127.0.0.1",
			"revision": "23.2025.05",
			"account_id": "5a78d28e-3b92-4d57-8ed1-2865d23c6df9"
		}
	],
	"trusted_contact": {
		"given_name": "Jane",
		"family_name": "Doe",
		"email_address": "gracious_newton_03124687@example.com"
	},
	"account_type": "trading",
	"trading_type": "margin",
	"trading_configurations": null,
	"enabled_assets": [
		"us_equity"
	]
}
```
Creating an ACH Relationship

In order to virtually fund an account via ACH we must first establish the ACH Relationship with the account.

We will be using the following endpoint POST /v1/accounts/{account_id}/ach_relationships  replacing the account_id with
5a78d28e-3b92-4d57-8ed1-2865d23c6df9.

Initially you will receive a QUEUED status. However, if you make a GET/v1/accounts/{account_id}/ach_relationships call after ~1 minute you should see an APPROVED status.

request body
```json
{
  "account_owner_name": "Gracious Newton",
  "bank_account_type": "CHECKING",
  "bank_account_number": "32131231abc",
  "bank_routing_number": "123103716",
  "nickname": "Bank of America Checking"
}
```
response
```json
{
	"id": "1e2b4032-9533-4317-b2dd-de56bd6c611a",
	"account_id": "5a78d28e-3b92-4d57-8ed1-2865d23c6df9",
	"created_at": "2025-10-26T22:47:45.43291284-04:00",
	"updated_at": "2025-10-26T22:47:45.43291284-04:00",
	"status": "QUEUED",
	"account_owner_name": "Gracious Newton",
	"bank_account_type": "CHECKING",
	"bank_account_number": "32131231abc",
	"bank_routing_number": "123103716",
	"nickname": "Bank of America Checking",
	"processor_token": null
}
```

Making a Virtual ACH Transfer

Now that you have an existing ACH relationship between the account and their bank, you can fund the account via ACH using the following endpoint POST /v1/accounts/{account_id}/transfers using the relationship_id we got in the response of the previous section.

request body
```json
{
  "transfer_type": "ach",
  "relationship_id": "1e2b4032-9533-4317-b2dd-de56bd6c611a",
  "amount": "1234.56",
  "direction": "INCOMING"
}
```

Passing an Order

The most common use case of Alpaca is to allow your end users to trade on the stock market. To do so simply pass in to
POST /v1/trading/accounts/{account_id}/orders and again replacing the account_id with  5a78d28e-3b92-4d57-8ed1-2865d23c6df9.

```json
{
  "transfer_type": "ach",
  "relationship_id": "1e2b4032-9533-4317-b2dd-de56bd6c611a",
  "amount": "1234.56",
  "direction": "INCOMING"
}
```
response
