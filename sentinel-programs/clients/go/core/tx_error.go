package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/solana-foundation/solana-go/v2"
)

var (
	anchorErrorLine = regexp.MustCompile(`^Program log: AnchorError .*Error Code: ([A-Za-z][A-Za-z0-9]*)\. Error Number: ([0-9]+)\. Error Message: (.*)$`)

	systemErrorDetails = []staticErrorDetail{
		{
			message: "account already in use",
			name:    "AccountAlreadyInUse",
		},
		{
			message: "account does not have enough SOL to perform the operation",
			name:    "ResultWithNegativeLamports",
		},
		{
			message: "cannot assign account to this program id",
			name:    "InvalidProgramId",
		},
		{
			message: "cannot allocate account data of this length",
			name:    "InvalidAccountDataLength",
		},
		{
			message: "length of requested seed is too long",
			name:    "MaxSeedLengthExceeded",
		},
		{
			message: "provided address does not match addressed derived from seed",
			name:    "AddressWithSeedMismatch",
		},
		{
			message: "advancing stored nonce requires a populated RecentBlockhashes sysvar",
			name:    "NonceNoRecentBlockhashes",
		},
		{
			message: "stored nonce is still in recent_blockhashes",
			name:    "NonceBlockhashNotExpired",
		},
		{
			message: "specified nonce does not match stored nonce",
			name:    "NonceUnexpectedBlockhashValue",
		},
	}

	tokenErrorDetails = []staticErrorDetail{
		{
			message: "lamport balance below rent-exempt threshold",
			name:    "NotRentExempt",
		},
		{
			message: "insufficient funds",
			name:    "InsufficientFunds",
		},
		{
			message: "invalid mint",
			name:    "InvalidMint",
		},
		{
			message: "account not associated with this mint",
			name:    "MintMismatch",
		},
		{
			message: "owner does not match",
			name:    "OwnerMismatch",
		},
		{
			message: "the total supply of this token is fixed",
			name:    "FixedSupply",
		},
		{
			message: "account or token already in use",
			name:    "AlreadyInUse",
		},
		{
			message: "invalid number of provided signers",
			name:    "InvalidNumberOfProvidedSigners",
		},
		{
			message: "invalid number of required signers",
			name:    "InvalidNumberOfRequiredSigners",
		},
		{
			message: "state is uninitialized",
			name:    "UninitializedState",
		},
		{
			message: "instruction does not support native tokens",
			name:    "NativeNotSupported",
		},
		{
			message: "non-native account can only be closed if its balance is zero",
			name:    "NonNativeHasBalance",
		},
		{
			message: "invalid instruction",
			name:    "InvalidInstruction",
		},
		{
			message: "invalid account state for operation",
			name:    "InvalidState",
		},
		{
			message: "operation overflowed",
			name:    "Overflow",
		},
		{
			message: "account does not support specified authority type",
			name:    "AuthorityTypeNotSupported",
		},
		{
			message: "this token mint cannot freeze accounts",
			name:    "MintCannotFreeze",
		},
		{
			message: "account is frozen",
			name:    "AccountFrozen",
		},
		{
			message: "decimals different from the Mint decimals",
			name:    "MintDecimalsMismatch",
		},
		{
			message: "instruction does not support non-native tokens",
			name:    "NonNativeNotSupported",
		},
	}
)

type TxError struct {
	Code    uint32
	Message string
	Name    string
	Program string
	Raw     string
}

func (e *TxError) Error() string {
	if e.Program == "" {
		return e.Raw
	}
	if e.Name == "" {
		return e.Program + " error: " + e.Raw
	}
	return fmt.Sprintf("%s error: %s (%d): %s", e.Program, e.Name, e.Code, e.Message)
}

type invocation struct {
	anchor  txErrorDetail
	cause   *invocation
	code    uint32
	hasCode bool
	program string
}

type staticErrorDetail struct {
	message string
	name    string
}

type txErrorDetail struct {
	code    uint32
	message string
	name    string
}

func DecodeTxError(txErr any, logs []string, programs []solana.PublicKey) *TxError {
	if txErr == nil {
		return nil
	}

	result := &TxError{Raw: fmt.Sprint(txErr)}
	data, err := json.Marshal(txErr)
	if err != nil {
		return result
	}
	var outer struct {
		InstructionError []json.RawMessage
	}
	if json.Unmarshal(data, &outer) != nil || len(outer.InstructionError) != 2 {
		return result
	}
	var index int
	if json.Unmarshal(outer.InstructionError[0], &index) != nil || index < 0 || index >= len(programs) {
		return result
	}
	var inner struct {
		Custom *uint32
	}
	if json.Unmarshal(outer.InstructionError[1], &inner) == nil && inner.Custom != nil {
		program, anchor := findFailedInvocation(logs, programs[index].String(), *inner.Custom)
		if program == "" {
			program = programs[index].String()
		}
		result.Program, result.Code = resolveProgramName(program), *inner.Custom
		if anchor.name != "" && anchor.code == *inner.Custom {
			result.Program, result.Name, result.Code, result.Message = resolveProgramName(program), anchor.name, anchor.code, anchor.message
			return result
		}
		if detail, ok := lookupStaticError(program, *inner.Custom); ok {
			result.Program, result.Name, result.Code, result.Message = resolveProgramName(program), detail.name, *inner.Custom, detail.message
		}
		return result
	}
	var name string
	if json.Unmarshal(outer.InstructionError[1], &name) == nil && name != "" {
		result.Program, result.Name = resolveProgramName(programs[index].String()), name
	}
	return result
}

func findFailedInvocation(logs []string, topProgram string, txCode uint32) (string, txErrorDetail) {
	var stack []invocation
	var standalone txErrorDetail
	for _, line := range logs {
		if match := anchorErrorLine.FindStringSubmatch(line); match != nil {
			if len(stack) > 0 {
				stack[len(stack)-1].cause = nil
			}
			code, err := strconv.ParseUint(match[2], 10, 32)
			if err == nil {
				detail := txErrorDetail{
					code:    uint32(code),
					message: match[3],
					name:    match[1],
				}
				if len(stack) > 0 {
					stack[len(stack)-1].anchor = detail
				} else {
					standalone = detail
				}
			}
			continue
		}
		if len(stack) > 0 && (strings.HasPrefix(line, "Program log:") || strings.HasPrefix(line, "Program data:") || strings.HasPrefix(line, "Program return:")) {
			stack[len(stack)-1].cause = nil
		}
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "Program" {
			continue
		}
		switch fields[2] {
		case "invoke":
			if len(fields) != 4 {
				continue
			}
			if fields[3] == "[1]" {
				stack = []invocation{{program: fields[1]}}
			} else if len(stack) > 0 && stack[0].program == topProgram {
				stack[len(stack)-1].cause = nil
				stack = append(stack, invocation{program: fields[1]})
			}
		case "success":
			if len(stack) > 0 && stack[len(stack)-1].program == fields[1] {
				stack = stack[:len(stack)-1]
			}
		case "failed:":
			if len(stack) > 0 && stack[0].program == topProgram && stack[len(stack)-1].program == fields[1] {
				failed := stack[len(stack)-1]
				failed.code, failed.hasCode = parseFailedCode(line)
				candidate := failed
				candidate.cause = nil
				if failed.cause != nil && failed.hasCode && failed.cause.hasCode && failed.code == failed.cause.code {
					candidate = *failed.cause
				}
				stack = stack[:len(stack)-1]
				if len(stack) > 0 {
					stack[len(stack)-1].cause = &candidate
					continue
				}
				if failed.hasCode && failed.code != txCode {
					return topProgram, txErrorDetail{}
				}
				return candidate.program, candidate.anchor
			}
			if len(stack) == 0 && fields[1] == topProgram {
				if code, ok := parseFailedCode(line); ok && code != txCode {
					return topProgram, txErrorDetail{}
				}
				return fields[1], standalone
			}
		}
	}
	if len(stack) == 0 {
		return "", standalone
	}
	return "", txErrorDetail{}
}

func lookupStaticError(program string, code uint32) (staticErrorDetail, bool) {
	var table []staticErrorDetail
	switch program {
	case solana.SystemProgramID.String():
		table = systemErrorDetails
	case solana.Token2022ProgramID.String():
		table = tokenErrorDetails
	}
	if code >= uint32(len(table)) {
		return staticErrorDetail{}, false
	}
	return table[code], true
}

func parseFailedCode(line string) (uint32, bool) {
	match := customErrorCode.FindStringSubmatch(line)
	if match == nil {
		return 0, false
	}
	code, err := strconv.ParseUint(match[1], 16, 32)
	return uint32(code), err == nil
}

func resolveProgramName(program string) string {
	switch program {
	case solana.SystemProgramID.String():
		return "system"
	case solana.Token2022ProgramID.String():
		return "token-2022"
	default:
		return program
	}
}
