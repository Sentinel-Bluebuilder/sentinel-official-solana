use crate::error::DvpnError;
use crate::events::GrantUsed;
use crate::state::Grant;
use anchor_lang::prelude::*;
use dvpn_core::constants::ALLOWANCE_UNLIMITED;

pub fn allowance_or_unlimited(allowance: Option<u64>) -> u64 {
    allowance.unwrap_or(ALLOWANCE_UNLIMITED)
}

pub fn charge_allowance(allowance: &mut u64, amount: u64) -> Result<()> {
    if *allowance == ALLOWANCE_UNLIMITED {
        return Ok(());
    }
    *allowance = allowance
        .checked_sub(amount)
        .ok_or_else(|| error!(DvpnError::AmountExceedsAllowance))?;
    Ok(())
}

#[must_use]
pub fn is_covered_by_grant(target: &Pubkey, keys: &[Pubkey]) -> bool {
    *target == Pubkey::default() || keys.contains(target)
}

pub fn require_owner_or_grantee(
    owner: &Pubkey,
    signer: &Pubkey,
    grant: Option<&Account<Grant>>,
    permission: u64,
    targets: &[Pubkey],
    now: i64,
) -> Result<()> {
    let Some(grant) = grant else {
        require_keys_eq!(*signer, *owner, DvpnError::Unauthorized);
        return Ok(());
    };
    require_keys_neq!(*signer, *owner, DvpnError::Unauthorized);
    require!(
        grant.granter == *owner && grant.grantee == *signer,
        DvpnError::Unauthorized
    );
    require!(
        grant.permissions & permission != 0,
        DvpnError::PermissionNotGranted
    );
    require!(!grant.is_expired(now), DvpnError::GrantExpired);
    require!(
        is_covered_by_grant(&grant.target, targets),
        DvpnError::GrantTargetMismatch
    );
    emit!(GrantUsed {
        grant: grant.key(),
        granter: grant.granter,
        grantee: grant.grantee,
        permission,
        target: grant.target,
    });
    Ok(())
}

#[cfg(test)]
mod tests {
    #![allow(clippy::unwrap_used, clippy::panic)]
    use super::*;
    use anchor_lang::error::Error;

    fn code(err: Error) -> u32 {
        match err {
            Error::AnchorError(error) => error.error_code_number,
            Error::ProgramError(error) => panic!("unexpected program error {error:?}"),
        }
    }

    fn custom(error: DvpnError) -> u32 {
        u32::from(error)
    }

    fn sample(
        owner: Pubkey,
        grantee: Pubkey,
        target: Pubkey,
        permissions: u64,
        expires_at: i64,
    ) -> Grant {
        Grant {
            granter: owner,
            grantee,
            target,
            permissions,
            expires_at,
            allowance: 0,
            rent_recipient: Pubkey::default(),
            created_at: 0,
            bump: 0,
            reserved: [0u8; 32],
        }
    }

    fn with_grant<F: FnOnce(&Account<Grant>)>(grant: Grant, check: F) {
        let key = Pubkey::new_unique();
        let mut data = Vec::new();
        grant.try_serialize(&mut data).unwrap();
        let mut lamports = 1_000_000u64;
        let program_id = crate::ID;
        let info = AccountInfo::new(
            &key,
            false,
            false,
            &mut lamports,
            &mut data,
            &program_id,
            false,
        );
        let account = Account::<Grant>::try_from(&info).unwrap();
        check(&account);
    }

    #[test]
    fn owner_needs_no_grant_but_an_owner_grant_is_rejected() {
        let owner = Pubkey::new_unique();
        assert!(require_owner_or_grantee(&owner, &owner, None, 1, &[], 0).is_ok());
        with_grant(
            sample(owner, Pubkey::new_unique(), Pubkey::default(), 1, 0),
            |grant| {
                assert_eq!(
                    code(
                        require_owner_or_grantee(&owner, &owner, Some(grant), 1, &[], 0)
                            .unwrap_err()
                    ),
                    custom(DvpnError::Unauthorized)
                );
            },
        );
    }

    #[test]
    fn stranger_without_grant_or_wrong_grantee_fails() {
        let owner = Pubkey::new_unique();
        let grantee = Pubkey::new_unique();
        assert_eq!(
            code(require_owner_or_grantee(&owner, &grantee, None, 1, &[], 0).unwrap_err()),
            custom(DvpnError::Unauthorized)
        );
        with_grant(
            sample(owner, Pubkey::new_unique(), Pubkey::default(), 1, 0),
            |grant| {
                assert_eq!(
                    code(
                        require_owner_or_grantee(&owner, &grantee, Some(grant), 1, &[], 0)
                            .unwrap_err()
                    ),
                    custom(DvpnError::Unauthorized)
                );
            },
        );
    }

    #[test]
    fn grant_must_cover_permission_expiry_and_target() {
        let owner = Pubkey::new_unique();
        let grantee = Pubkey::new_unique();
        let node = Pubkey::new_unique();
        with_grant(
            sample(owner, grantee, Pubkey::default(), 0b10, 100),
            |grant| {
                assert!(
                    require_owner_or_grantee(&owner, &grantee, Some(grant), 0b10, &[], 99).is_ok()
                );
                assert_eq!(
                    code(
                        require_owner_or_grantee(&owner, &grantee, Some(grant), 0b01, &[], 99)
                            .unwrap_err()
                    ),
                    custom(DvpnError::PermissionNotGranted)
                );
                assert_eq!(
                    code(
                        require_owner_or_grantee(&owner, &grantee, Some(grant), 0b10, &[], 100)
                            .unwrap_err()
                    ),
                    custom(DvpnError::GrantExpired)
                );
            },
        );
        with_grant(sample(owner, grantee, node, 1, 0), |grant| {
            assert!(require_owner_or_grantee(&owner, &grantee, Some(grant), 1, &[node], 0).is_ok());
            assert_eq!(
                code(
                    require_owner_or_grantee(
                        &owner,
                        &grantee,
                        Some(grant),
                        1,
                        &[Pubkey::new_unique()],
                        0
                    )
                    .unwrap_err()
                ),
                custom(DvpnError::GrantTargetMismatch)
            );
        });
    }

    #[test]
    fn allowance_charged_and_unlimited_skipped() {
        let mut allowance = 100u64;
        charge_allowance(&mut allowance, 40).unwrap();
        assert_eq!(allowance, 60);
        assert!(charge_allowance(&mut allowance, 61).is_err());
        let mut exhausted = 0u64;
        assert_eq!(
            code(charge_allowance(&mut exhausted, 1).unwrap_err()),
            custom(DvpnError::AmountExceedsAllowance)
        );
        let mut unlimited = ALLOWANCE_UNLIMITED;
        charge_allowance(&mut unlimited, u64::MAX).unwrap();
        assert_eq!(unlimited, ALLOWANCE_UNLIMITED);
    }
}
