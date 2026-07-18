Import reconstructs journal/accounts/balances but never repopulates the idempotency key map, so replaying a key after import re-applies the transaction.
