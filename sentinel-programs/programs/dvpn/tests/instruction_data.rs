use anchor_lang::{Discriminator, InstructionData};

// Borsh lays out fields in declaration order, so these bytes pin the wire format
// of the governance instructions independently of how the arguments are declared.
#[test]
fn initialize_config_data_is_discriminator_then_fields_in_order() {
    let data = dvpn::instruction::InitializeConfig {
        fee_bps: 0x0102,
        min_node_bond: 0x0304_0506_0708_090a,
        min_provider_bond: 0x0b0c_0d0e_0f10_1112,
        unbonding_seconds: 0x1314_1516,
        channel_close_grace_seconds: 0x1718_191a,
        lease_close_grace_seconds: 0x1b1c_1d1e,
        node_listing_ttl_seconds: 0x1f20_2122,
        paused_mask: 0x23,
    }
    .data();

    let mut expected = dvpn::instruction::InitializeConfig::DISCRIMINATOR.to_vec();
    expected.extend_from_slice(&0x0102_u16.to_le_bytes());
    expected.extend_from_slice(&0x0304_0506_0708_090a_u64.to_le_bytes());
    expected.extend_from_slice(&0x0b0c_0d0e_0f10_1112_u64.to_le_bytes());
    expected.extend_from_slice(&0x1314_1516_u32.to_le_bytes());
    expected.extend_from_slice(&0x1718_191a_u32.to_le_bytes());
    expected.extend_from_slice(&0x1b1c_1d1e_u32.to_le_bytes());
    expected.extend_from_slice(&0x1f20_2122_u32.to_le_bytes());
    expected.push(0x23);
    assert_eq!(data, expected);
}

#[test]
fn set_params_data_is_discriminator_then_optional_fields_in_order() {
    let data = dvpn::instruction::SetParams {
        fee_bps: Some(0x0102),
        min_node_bond: None,
        min_provider_bond: Some(0x0b0c_0d0e_0f10_1112),
        unbonding_seconds: None,
        channel_close_grace_seconds: Some(0x1718_191a),
        lease_close_grace_seconds: None,
        node_listing_ttl_seconds: Some(0x1f20_2122),
    }
    .data();

    let mut expected = dvpn::instruction::SetParams::DISCRIMINATOR.to_vec();
    expected.push(1);
    expected.extend_from_slice(&0x0102_u16.to_le_bytes());
    expected.push(0);
    expected.push(1);
    expected.extend_from_slice(&0x0b0c_0d0e_0f10_1112_u64.to_le_bytes());
    expected.push(0);
    expected.push(1);
    expected.extend_from_slice(&0x1718_191a_u32.to_le_bytes());
    expected.push(0);
    expected.push(1);
    expected.extend_from_slice(&0x1f20_2122_u32.to_le_bytes());
    assert_eq!(data, expected);
}
