import SwiftUI
import UniformTypeIdentifiers

/// Màn "Tạo USB" — các thao tác ghi phá huỷ kiểu Rufus: ghi image, format, kiểm
/// tra bad block. Tách hẳn khỏi luồng cứu hộ; luôn bắt người dùng chọn whole disk
/// gắn ngoài và gõ lại tên ổ trước khi ghi.
struct CreateUSBView: View {
    let disks: [Disk]
    @State private var ctrl = ImagingController()
    @State private var showImporter = false
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    diskPicker
                    if ctrl.target != nil {
                        actionPicker
                        optionsSection
                        if needsConfirm { confirmSection }
                        runSection
                    }
                }
                .padding(20)
                // Đổi tab (Ghi image / Format / Kiểm tra ổ) không trượt/mờ dần:
                // tắt animation ngầm theo thao tác để nội dung đứng yên, chuyển
                // tức thì thay vì "chuyển động theo".
                .animation(nil, value: ctrl.action)
                .animation(nil, value: ctrl.writeTest)
                .animation(nil, value: ctrl.fs)
                .animation(nil, value: ctrl.targetBSD)
            }
        }
        // Cao cố định đủ chứa tab cao nhất (Format) với vài ổ, để KHÔNG tab nào
        // sinh thanh cuộn và chuyển tab vẫn đứng yên (chiều cao không đổi). Tab
        // ngắn hơn để trống một chút ở dưới — chấp nhận, đổi lấy sự tĩnh.
        .frame(width: 520, height: 740)
        .fileImporter(isPresented: $showImporter, allowedContentTypes: imageTypes) { result in
            if case .success(let url) = result { ctrl.imagePath = url.path }
        }
    }

    private var needsConfirm: Bool {
        ctrl.action.destructive || (ctrl.action == .badBlocks && ctrl.writeTest)
    }

    /// Nhãn volume của ổ đang chọn (để hiển thị), lùi về bsd nếu không có.
    private var selectedDiskName: String {
        disks.first(where: { $0.bsd == ctrl.targetBSD })?.friendlyName
            ?? ctrl.target?.bsd ?? ""
    }

    // MARK: đầu trang

    private var header: some View {
        HStack(spacing: 10) {
            Image(systemName: "externaldrive.badge.plus").font(.title2).foregroundStyle(Brand.primary)
            VStack(alignment: .leading, spacing: 1) {
                Text("Tạo USB").font(.headline)
                Text("Ghi image · Format · Kiểm tra ổ").font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            Button("Đóng") { dismiss() }.keyboardShortcut(.cancelAction)
        }
        .padding(.horizontal, 18).padding(.vertical, 12)
    }

    // MARK: chọn ổ

    private var diskPicker: some View {
        SectionCard(title: "Ổ đích", systemImage: "externaldrive") {
            if disks.isEmpty {
                Text("Chưa có ổ gắn ngoài nào. Cắm USB rồi thử lại.")
                    .font(.callout).foregroundStyle(.secondary)
            } else {
                VStack(spacing: 8) {
                    ForEach(disks) { disk in
                        diskRow(disk)
                    }
                }
                Label("Mọi thao tác ở đây XOÁ SẠCH dữ liệu trên ổ được chọn.",
                      systemImage: "exclamationmark.triangle.fill")
                    .font(.caption).foregroundStyle(Brand.warn)
                    .padding(.top, 4)
            }
        }
    }

    private func diskRow(_ disk: Disk) -> some View {
        let selected = ctrl.targetBSD == disk.bsd
        return Button {
            Task { await ctrl.preflight(bsd: disk.bsd) }
        } label: {
            HStack(spacing: 11) {
                Image(systemName: "externaldrive.fill")
                    .foregroundStyle(selected ? .white : Brand.primary)
                VStack(alignment: .leading, spacing: 2) {
                    Text(disk.friendlyName)
                        .fontWeight(.semibold).lineLimit(1)
                    Text("\(disk.bsd) · \(formatBytes(disk.sizeBytes))\(disk.busProtocol.isEmpty ? "" : " · \(disk.busProtocol)")")
                        .font(.caption2).foregroundStyle(selected ? .white.opacity(0.85) : .secondary)
                }
                Spacer(minLength: 0)
                if selected { Image(systemName: "checkmark.circle.fill").foregroundStyle(.white) }
            }
            .padding(10)
            .background {
                RoundedRectangle(cornerRadius: 10)
                    .fill(selected ? AnyShapeStyle(Brand.heroGradient) : AnyShapeStyle(Color(nsColor: .controlBackgroundColor)))
            }
            .overlay(RoundedRectangle(cornerRadius: 10).stroke(.primary.opacity(selected ? 0 : 0.07)))
            .foregroundStyle(selected ? .white : .primary)
        }
        .buttonStyle(.plain)
    }

    // MARK: chọn thao tác

    private var actionPicker: some View {
        Picker("", selection: $ctrl.action) {
            ForEach(ImagingController.Action.allCases) { a in
                Label(a.title, systemImage: a.icon).tag(a)
            }
        }
        .pickerStyle(.segmented)
        .labelsHidden()
    }

    // MARK: tuỳ chọn theo thao tác

    @ViewBuilder private var optionsSection: some View {
        switch ctrl.action {
        case .flash:
            SectionCard(title: "Image nguồn", systemImage: "doc") {
                HStack(spacing: 8) {
                    Text(ctrl.imagePath.isEmpty ? "Chưa chọn file" : (ctrl.imagePath as NSString).lastPathComponent)
                        .font(.callout).foregroundStyle(ctrl.imagePath.isEmpty ? .secondary : .primary)
                        .lineLimit(1).truncationMode(.middle)
                    Spacer()
                    Button("Chọn...") { showImporter = true }
                }
                Toggle("Kiểm chứng sau khi ghi (SHA-256)", isOn: $ctrl.verify)
                    .font(.callout)
            }
        case .format:
            SectionCard(title: "Định dạng", systemImage: "eraser") {
                Picker("Filesystem", selection: $ctrl.fs) {
                    Text("exFAT").tag("exfat")
                    Text("FAT32").tag("fat32")
                    Text("NTFS").tag("ntfs")
                }
                Picker("Sơ đồ phân vùng", selection: $ctrl.scheme) {
                    Text("GPT").tag("gpt")
                    Text("MBR").tag("mbr")
                }
                .disabled(ctrl.fs == "ntfs") // NTFS hiện chỉ GPT
                HStack {
                    Text("Nhãn")
                    TextField("USB", text: $ctrl.label)
                }
                if ctrl.fs == "ntfs" {
                    Text("NTFS chỉ hỗ trợ GPT và cần mkntfs đóng gói kèm app.")
                        .font(.caption2).foregroundStyle(.secondary)
                }
            }
        case .badBlocks:
            SectionCard(title: "Kiểm tra ổ", systemImage: "stethoscope") {
                Toggle("Ghi-thử (phá dữ liệu, kỹ hơn)", isOn: $ctrl.writeTest)
                    .font(.callout)
                Text(ctrl.writeTest
                     ? "Ghi pattern rồi đọc lại toàn ổ — xoá sạch dữ liệu."
                     : "Chỉ đọc toàn ổ để tìm sector lỗi — không đụng dữ liệu.")
                    .font(.caption2).foregroundStyle(.secondary)
            }
        }
    }

    // MARK: xác nhận

    private var confirmSection: some View {
        SectionCard(title: "Xác nhận xoá", systemImage: "exclamationmark.shield") {
            if let t = ctrl.target {
                // Không bắt gõ lại tên nữa: một ô tick, hiện rõ tên ổ. Khi tick,
                // tự điền đúng token để dịch vụ nền (vẫn kiểm tra token phía sau)
                // chấp nhận. Vẫn còn một bước cố ý để không lỡ tay xoá nhầm.
                Toggle(isOn: Binding(
                    get: { ctrl.confirmInput == t.confirmToken },
                    set: { ctrl.confirmInput = $0 ? t.confirmToken : "" }
                )) {
                    (Text("Tôi hiểu ổ ")
                        + Text(selectedDiskName).bold().foregroundColor(Brand.warn)
                        + Text(" (\(t.bsd)) sẽ bị XOÁ SẠCH toàn bộ dữ liệu."))
                        .font(.callout)
                }
                .tint(Brand.warn)
            }
        }
    }

    // MARK: chạy + kết quả

    private var runSection: some View {
        VStack(spacing: 12) {
            if ctrl.phase == .running {
                VStack(spacing: 6) {
                    ProgressView(value: ctrl.progress)
                    Text("\(ctrl.statusText) \(Int(ctrl.progress * 100))%")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            if let msg = ctrl.resultText, ctrl.phase == .done {
                Label(msg, systemImage: "checkmark.seal.fill")
                    .font(.callout).foregroundStyle(Brand.safe)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            if let err = ctrl.errorText, ctrl.phase == .failed {
                Label(err, systemImage: "xmark.octagon.fill")
                    .font(.callout).foregroundStyle(Brand.warn)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            BrandButton(title: runTitle, icon: ctrl.action.icon, tint: Brand.primary) {
                Task { await ctrl.run() }
            }
            .disabled(!ctrl.canRun)
            .opacity(ctrl.canRun ? 1 : 0.5)
        }
    }

    private var runTitle: String {
        switch ctrl.action {
        case .flash: return "Ghi image ra ổ"
        case .format: return "Format ổ"
        case .badBlocks: return ctrl.writeTest ? "Ghi-thử toàn ổ" : "Quét bad block"
        }
    }

    private var imageTypes: [UTType] {
        var t: [UTType] = [.diskImage, .data]
        if let iso = UTType(filenameExtension: "iso") { t.insert(iso, at: 0) }
        if let img = UTType(filenameExtension: "img") { t.insert(img, at: 0) }
        return t
    }
}
