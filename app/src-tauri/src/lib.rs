// The app is a view over the bundled `calport` binary: every action runs it as
// a sidecar with --json, so all behaviour lives in one place, shared with the CLI.
#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_opener::init())
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
