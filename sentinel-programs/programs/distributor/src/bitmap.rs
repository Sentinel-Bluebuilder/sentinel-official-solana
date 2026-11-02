#[derive(Debug, PartialEq, Eq)]
pub enum BitError {
    AlreadySet,
    OutOfRange,
}

#[must_use]
pub fn is_set(bits: &[u8], index: u32) -> bool {
    let byte = (index / 8) as usize;
    bits.get(byte)
        .is_some_and(|byte_value| (byte_value >> (index % 8)) & 1 == 1)
}

pub fn try_set(bits: &mut [u8], index: u32) -> Result<(), BitError> {
    let byte = (index / 8) as usize;
    let Some(slot) = bits.get_mut(byte) else {
        return Err(BitError::OutOfRange);
    };
    let mask = 1u8 << (index % 8);
    if *slot & mask != 0 {
        return Err(BitError::AlreadySet);
    }
    *slot |= mask;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn get_and_set_bits() {
        let mut bits = vec![0u8; 2];
        assert!(!is_set(&bits, 0));
        assert!(try_set(&mut bits, 0).is_ok());
        assert!(is_set(&bits, 0));
        assert!(try_set(&mut bits, 9).is_ok());
        assert!(is_set(&bits, 9));
        assert!(!is_set(&bits, 8));
    }

    #[test]
    fn try_set_distinguishes_already_set_from_out_of_range() {
        let mut bits = vec![0u8; 1];
        assert!(try_set(&mut bits, 3).is_ok());
        assert_eq!(try_set(&mut bits, 3), Err(BitError::AlreadySet));
        assert_eq!(try_set(&mut bits, 8), Err(BitError::OutOfRange));
    }
}
