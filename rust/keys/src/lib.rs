#![deny(warnings)]
#![deny(unsafe_op_in_unsafe_fn)]

mod zip316;

use base64::Engine as _;
use core::ffi::c_char;
use orchard::keys::{FullViewingKey, Scope, SpendingKey};
use serde::{Deserialize, Serialize};
use thiserror::Error;
use zeroize::Zeroize;

const HRP_UFVK_PREFIX: &str = "jview";
const TYPECODE_ORCHARD: u64 = 3;
const ORCHARD_FVK_LEN: usize = 96;

#[derive(Debug, Error)]
enum KeysError {
    #[error("req_json_invalid")]
    ReqJSONInvalid,
    #[error("seed_invalid")]
    SeedInvalid,
    #[error("ua_hrp_invalid")]
    UAHrpInvalid,
    #[error("ufvk_invalid")]
    UFVKInvalid,
    #[error("scope_invalid")]
    ScopeInvalid,
    #[error("coin_type_invalid")]
    CoinTypeInvalid,
    #[error("account_invalid")]
    AccountInvalid,
    #[error("internal")]
    Internal,
    #[error("panic")]
    Panic,
}

#[derive(Debug, Deserialize)]
struct UFVKFromSeedRequest {
    seed_base64: String,
    ua_hrp: String,
    coin_type: u32,
    account: u32,
}

#[derive(Debug, Deserialize)]
struct AddressFromUFVKRequest {
    ufvk: String,
    ua_hrp: String,
    scope: String,
    index: u32,
}

#[derive(Serialize)]
#[serde(tag = "status", rename_all = "snake_case")]
enum UFVKFromSeedResponse {
    Ok { ufvk: String },
    Err { error: String },
}

#[derive(Serialize)]
#[serde(tag = "status", rename_all = "snake_case")]
enum AddressFromUFVKResponse {
    Ok { address: String },
    Err { error: String },
}

fn to_c_string<T: Serialize>(v: &T) -> *mut c_char {
    let json = serde_json::to_string(v)
        .unwrap_or_else(|_| r#"{"status":"err","error":"internal"}"#.to_string());
    std::ffi::CString::new(json).expect("json").into_raw()
}

fn decode_seed(seed_base64: &str) -> Result<Vec<u8>, KeysError> {
    let bytes = base64::engine::general_purpose::STANDARD
        .decode(seed_base64.trim())
        .map_err(|_| KeysError::SeedInvalid)?;
    if !(32..=252).contains(&bytes.len()) {
        return Err(KeysError::SeedInvalid);
    }
    Ok(bytes)
}

fn ufvk_hrp_from_ua_hrp(ua_hrp: &str) -> Result<String, KeysError> {
    let hrp = ua_hrp.trim();
    if hrp.is_empty() {
        return Err(KeysError::UAHrpInvalid);
    }
    if hrp == "j" {
        return Ok("jview".to_string());
    }
    let Some(suffix) = hrp.strip_prefix('j') else {
        return Err(KeysError::UAHrpInvalid);
    };
    if suffix.is_empty() {
        return Ok("jview".to_string());
    }
    Ok(format!("jview{suffix}"))
}

fn parse_scope(s: &str) -> Result<Scope, KeysError> {
    match s.trim().to_ascii_lowercase().as_str() {
        "external" => Ok(Scope::External),
        "internal" => Ok(Scope::Internal),
        _ => Err(KeysError::ScopeInvalid),
    }
}

fn parse_orchard_fvk_from_ufvk(ufvk: &str) -> Result<FullViewingKey, KeysError> {
    let ufvk = ufvk.trim();
    if ufvk.is_empty() {
        return Err(KeysError::UFVKInvalid);
    }
    let (hrp, _) = ufvk.split_once('1').ok_or(KeysError::UFVKInvalid)?;
    if !hrp.starts_with(HRP_UFVK_PREFIX) {
        return Err(KeysError::UFVKInvalid);
    }

    let items = zip316::decode_tlv_container(hrp, ufvk).map_err(|_| KeysError::UFVKInvalid)?;
    let orchard_item = items
        .into_iter()
        .find(|(typecode, _)| *typecode == TYPECODE_ORCHARD)
        .ok_or(KeysError::UFVKInvalid)?;
    if orchard_item.1.len() != ORCHARD_FVK_LEN {
        return Err(KeysError::UFVKInvalid);
    }
    let fvk_bytes: [u8; ORCHARD_FVK_LEN] = orchard_item
        .1
        .try_into()
        .map_err(|_| KeysError::UFVKInvalid)?;
    FullViewingKey::from_bytes(&fvk_bytes).ok_or(KeysError::UFVKInvalid)
}

fn ufvk_from_seed(req: &UFVKFromSeedRequest) -> Result<String, KeysError> {
    if req.coin_type >= 0x8000_0000 {
        return Err(KeysError::CoinTypeInvalid);
    }
    if req.account >= 0x8000_0000 {
        return Err(KeysError::AccountInvalid);
    }

    let ufvk_hrp = ufvk_hrp_from_ua_hrp(&req.ua_hrp)?;

    let mut seed = decode_seed(&req.seed_base64)?;
    let account = zip32::AccountId::try_from(req.account).map_err(|_| KeysError::AccountInvalid)?;
    let sk = SpendingKey::from_zip32_seed(&seed, req.coin_type, account).map_err(|_| KeysError::SeedInvalid)?;
    seed.zeroize();

    let fvk = FullViewingKey::from(&sk);
    let fvk_bytes = fvk.to_bytes();

    zip316::encode_unified_container(&ufvk_hrp, TYPECODE_ORCHARD, &fvk_bytes)
        .map_err(|_| KeysError::Internal)
}

fn address_from_ufvk(req: &AddressFromUFVKRequest) -> Result<String, KeysError> {
    let ua_hrp = req.ua_hrp.trim();
    if ua_hrp.is_empty() {
        return Err(KeysError::UAHrpInvalid);
    }
    let fvk = parse_orchard_fvk_from_ufvk(&req.ufvk)?;
    let scope = parse_scope(&req.scope)?;
    let addr = fvk.address_at(req.index, scope);
    let raw = addr.to_raw_address_bytes();
    zip316::encode_unified_container(ua_hrp, TYPECODE_ORCHARD, &raw).map_err(|_| KeysError::Internal)
}

#[no_mangle]
pub extern "C" fn juno_keys_ufvk_from_seed_json(req_json: *const c_char) -> *mut c_char {
    let res = std::panic::catch_unwind(|| {
        if req_json.is_null() {
            return Err(KeysError::ReqJSONInvalid);
        }
        let s = unsafe { std::ffi::CStr::from_ptr(req_json) }.to_string_lossy();
        let req: UFVKFromSeedRequest =
            serde_json::from_str(&s).map_err(|_| KeysError::ReqJSONInvalid)?;
        let ufvk = ufvk_from_seed(&req)?;
        Ok(UFVKFromSeedResponse::Ok { ufvk })
    });
    match res {
        Ok(Ok(v)) => to_c_string(&v),
        Ok(Err(e)) => to_c_string(&UFVKFromSeedResponse::Err {
            error: e.to_string(),
        }),
        Err(_) => to_c_string(&UFVKFromSeedResponse::Err {
            error: KeysError::Panic.to_string(),
        }),
    }
}

#[no_mangle]
pub extern "C" fn juno_keys_address_from_ufvk_json(req_json: *const c_char) -> *mut c_char {
    let res = std::panic::catch_unwind(|| {
        if req_json.is_null() {
            return Err(KeysError::ReqJSONInvalid);
        }
        let s = unsafe { std::ffi::CStr::from_ptr(req_json) }.to_string_lossy();
        let req: AddressFromUFVKRequest =
            serde_json::from_str(&s).map_err(|_| KeysError::ReqJSONInvalid)?;
        let address = address_from_ufvk(&req)?;
        Ok(AddressFromUFVKResponse::Ok { address })
    });
    match res {
        Ok(Ok(v)) => to_c_string(&v),
        Ok(Err(e)) => to_c_string(&AddressFromUFVKResponse::Err {
            error: e.to_string(),
        }),
        Err(_) => to_c_string(&AddressFromUFVKResponse::Err {
            error: KeysError::Panic.to_string(),
        }),
    }
}

#[no_mangle]
pub extern "C" fn juno_keys_string_free(s: *mut c_char) {
    if s.is_null() {
        return;
    }
    unsafe {
        drop(std::ffi::CString::from_raw(s));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn derives_ufvk_and_addresses_regtest() {
        let seed = [7u8; 64];
        let seed_b64 = base64::engine::general_purpose::STANDARD.encode(seed);

        let req = UFVKFromSeedRequest {
            seed_base64: seed_b64,
            ua_hrp: "jregtest".to_string(),
            coin_type: 8135,
            account: 0,
        };

        let ufvk = ufvk_from_seed(&req).expect("ufvk");
        assert!(ufvk.starts_with("jviewregtest1"));

        let fvk = parse_orchard_fvk_from_ufvk(&ufvk).expect("decode ufvk");

        let addr0 = address_from_ufvk(&AddressFromUFVKRequest {
            ufvk: ufvk.clone(),
            ua_hrp: "jregtest".to_string(),
            scope: "external".to_string(),
            index: 0,
        })
        .expect("addr0");
        let expected0 = zip316::encode_unified_container(
            "jregtest",
            TYPECODE_ORCHARD,
            &fvk.address_at(0u32, Scope::External).to_raw_address_bytes(),
        )
        .expect("expected0");
        assert_eq!(addr0, expected0);

        let addr0_internal = address_from_ufvk(&AddressFromUFVKRequest {
            ufvk,
            ua_hrp: "jregtest".to_string(),
            scope: "internal".to_string(),
            index: 0,
        })
        .expect("addr0_internal");
        let expected0_internal = zip316::encode_unified_container(
            "jregtest",
            TYPECODE_ORCHARD,
            &fvk.address_at(0u32, Scope::Internal).to_raw_address_bytes(),
        )
        .expect("expected0_internal");
        assert_eq!(addr0_internal, expected0_internal);
    }
}
