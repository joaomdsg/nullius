Import removes the field-count check on transaction lines before indexing fields[1..6], so a truncated/short line causes an index-out-of-range panic instead of ErrCorruptImport.
