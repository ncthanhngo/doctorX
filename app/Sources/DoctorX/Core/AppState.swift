import Foundation
import Observation

/// Trạng thái dùng chung của toàn app.
@MainActor
@Observable
final class AppState {
    var disks: [Disk] = []
    var selected: Partition?
    var scanResult: ScanResult?

    var isBusy = false
    var busyMessage = ""
    var errorMessage: String?
    var lastRescueSummary: String?

    /// Mã journal của lần gỡ cờ ẩn gần nhất, để bật nút Hoàn tác.
    var lastJournalID: String?

    private let client = CoreClient()

    var isDaemonReachable = false

    func refreshDevices() async {
        if DemoData.isEnabled {
            disks = DemoData.disks()
            isDaemonReachable = true
            // Cho phép tự chọn sẵn một ổ khi xem trước giao diện (chỉ demo).
            if selected == nil, let want = ProcessInfo.processInfo.environment["DOCTORX_DEMO_SELECT"],
               let p = disks.flatMap(\.partitions).first(where: { $0.bsd == want }) {
                select(p)
            }
            return
        }
        do {
            // KHÔNG bọc trong run{}: đây là poll nền chạy mỗi 30s và mỗi lần
            // cắm/rút ổ. Bật overlay "đang bận" ở đây sẽ làm màn hình nhấp nháy
            // liên tục. Chạy im lặng.
            let result = try await self.client.call(method: "list_devices")
            let raw = result["disks"] as? [[String: Any]] ?? []
            let newDisks = raw.compactMap(Disk.init)
            isDaemonReachable = true

            // Chỉ cập nhật khi danh sách thực sự đổi — tránh SwiftUI vẽ lại toàn
            // bộ sidebar mỗi vòng poll dù không có gì thay đổi.
            if newDisks != disks {
                disks = newDisks
            }

            // Giữ lựa chọn cũ nếu ổ vẫn còn cắm.
            if let sel = selected,
               !disks.contains(where: { $0.partitions.contains(where: { $0.bsd == sel.bsd }) }) {
                selected = nil
                scanResult = nil
            }
        } catch {
            isDaemonReachable = false
        }
    }

    func select(_ partition: Partition) {
        selected = partition
        scanResult = nil
        lastJournalID = nil
        Task { await scan() }
    }

    func scan() async {
        guard let part = selected else { return }
        if DemoData.isEnabled {
            // Ổ NTFS lớn coi như sạch, còn lại coi như nhiễm — để xem cả hai
            // trạng thái giao diện.
            scanResult = part.fsType == "ntfs" ? DemoData.cleanScan() : DemoData.infectedScan()
            return
        }
        busyMessage = "Đang quét \(part.displayName)..."
        do {
            let result = try await run {
                try await self.client.call(method: "scan_volume", params: ["bsd": part.bsd])
            }
            var out = ScanResult()
            out.concealed = (result["concealed"] as? [[String: Any]] ?? []).compactMap(ConcealedItem.init)
            out.usedRawDriver = result["rawDriver"] as? Bool ?? true
            if let worm = result["worm"] as? [String: Any] {
                out.findings = (worm["findings"] as? [[String: Any]] ?? []).compactMap(WormFinding.init)
                out.severity = worm["severity"] as? String ?? "clean"
            }
            scanResult = out
        } catch {
            report(error)
        }
    }

    /// Gỡ cờ Hidden+System — bản chuyển của `attrib -h -s /s /d`.
    func unhide(path: String, recursive: Bool) async {
        guard let part = selected else { return }
        busyMessage = "Đang khôi phục \(path)..."
        do {
            let result = try await run {
                try await self.client.call(
                    method: "rescue_unhide",
                    params: ["bsd": part.bsd, "path": path, "recursive": recursive]
                )
            }
            let changed = result["entriesChanged"] as? Int ?? 0
            lastJournalID = result["journalId"] as? String
            lastRescueSummary = changed > 0
                ? "Đã khôi phục \(changed) mục. Mở Finder để kiểm tra."
                : "Không có mục nào cần khôi phục."
            await scan()
        } catch {
            report(error)
        }
    }

    /// Sao chép dữ liệu ra nơi an toàn. Không ghi gì lên ổ nguồn.
    func copyOut(paths: [String], dest: String?) async {
        guard let part = selected else { return }
        busyMessage = "Đang sao chép..."
        do {
            var params: [String: Any] = ["bsd": part.bsd, "paths": paths]
            if let dest { params["dest"] = dest }
            let result = try await run {
                try await self.client.call(method: "rescue_copy_out", params: params) { _, data in
                    if let file = data["file"] as? String {
                        Task { @MainActor in
                            self.busyMessage = "Đang sao chép \((file as NSString).lastPathComponent)"
                        }
                    }
                }
            }
            let files = result["filesCopied"] as? Int ?? 0
            let bytes = (result["bytesCopied"] as? NSNumber)?.int64Value ?? 0
            lastRescueSummary = "Đã cứu \(files) file (\(formatBytes(bytes)))."
        } catch {
            report(error)
        }
    }

    /// Bọc thao tác nền: bật cờ bận, tự tắt khi xong.
    private func run<T>(_ body: @escaping () async throws -> T) async throws -> T {
        isBusy = true
        defer { isBusy = false; busyMessage = "" }
        return try await body()
    }

    private func report(_ error: Error) {
        errorMessage = error.localizedDescription
    }
}
