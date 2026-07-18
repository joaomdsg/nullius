Post mutates account balances directly inside the validation loop, so a later currency failing the balance check leaves earlier accounts' balances already changed (partial apply on error).
