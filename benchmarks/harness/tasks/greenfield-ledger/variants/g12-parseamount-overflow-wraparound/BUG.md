ParseAmount drops the range check on the parsed big.Int, so out-of-range values silently wrap around via big.Int.Int64() truncation instead of returning ErrOverflow.
