import Foundation
import DiskArbitration

/// Theo dõi sự kiện cắm/rút ổ đĩa qua DiskArbitration, thay cho polling định kỳ.
///
/// Dùng `DASessionSetDispatchQueue` thay vì schedule vào run loop: đơn giản hơn,
/// không phụ thuộc app có đang chạy run loop kiểu nào (SwiftUI/AppKit), và tự
/// động dùng một dispatch queue riêng nên không tốn thêm gì trên main thread.
final class DeviceMonitor {
    private var session: DASession?
    private var onChange: (() -> Void)?
    private let queue = DispatchQueue(label: "com.doctorx.diskarbitration")

    /// Debounce: gộp nhiều callback liên tiếp (ổ nhiều partition) thành 1 lần refresh.
    private var debounceWorkItem: DispatchWorkItem?
    private let debounceInterval: TimeInterval = 0.3

    /// Bắt đầu theo dõi. Nếu DiskArbitration không khởi tạo được session,
    /// hàm coi như no-op — app sẽ chỉ còn fallback poll chậm ở ContentView.
    func start(onChange: @escaping () -> Void) {
        guard session == nil else { return }
        guard let session = DASessionCreate(kCFAllocatorDefault) else {
            return
        }
        self.onChange = onChange
        self.session = session

        let context = Unmanaged.passUnretained(self).toOpaque()
        DARegisterDiskAppearedCallback(session, nil, diskChangedCallback, context)
        DARegisterDiskDisappearedCallback(session, nil, diskChangedCallback, context)

        DASessionSetDispatchQueue(session, queue)
    }

    /// Dừng theo dõi và giải phóng session.
    func stop() {
        guard let session else { return }
        debounceWorkItem?.cancel()
        debounceWorkItem = nil
        DASessionSetDispatchQueue(session, nil)
        self.session = nil
        self.onChange = nil
    }

    deinit {
        stop()
    }

    /// Gộp sự kiện rồi gọi callback trên main actor (AppState là @MainActor).
    fileprivate func handleDiskEvent() {
        debounceWorkItem?.cancel()
        let work = DispatchWorkItem { [weak self] in
            guard let self else { return }
            DispatchQueue.main.async {
                self.onChange?()
            }
        }
        debounceWorkItem = work
        queue.asyncAfter(deadline: .now() + debounceInterval, execute: work)
    }
}

/// Callback C cho cả disk appeared/disappeared — không cần phân biệt loại sự kiện,
/// vì mọi thay đổi đều dẫn tới cùng một hành động: refresh danh sách ổ.
private func diskChangedCallback(disk: DADisk, context: UnsafeMutableRawPointer?) {
    guard let context else { return }
    let monitor = Unmanaged<DeviceMonitor>.fromOpaque(context).takeUnretainedValue()
    monitor.handleDiskEvent()
}
