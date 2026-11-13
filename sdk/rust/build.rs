// Builds the C library and links it statically: from ../c beside this crate in the repository, or from c/ inside
// a packaged copy of the crate (the release archives, crates.io).
use std::path::Path;

fn main() {
    let c = if Path::new("c/src/gs_sdk.c").exists() { "c" } else { "../c" };
    let mut b = cc::Build::new();
    b.file(format!("{c}/src/gs_sdk.c")).include(format!("{c}/include")).warnings(true);
    let target = std::env::var("CARGO_CFG_TARGET_OS").unwrap_or_default();
    if target != "windows" {
        b.define("_POSIX_C_SOURCE", "200809L");
    }
    b.compile("gs_sdk");
    if target == "windows" {
        println!("cargo:rustc-link-lib=ws2_32");
    } else {
        println!("cargo:rustc-link-lib=pthread");
    }
    println!("cargo:rerun-if-changed={c}/src/gs_sdk.c");
    println!("cargo:rerun-if-changed={c}/include/gs_sdk.h");
}
