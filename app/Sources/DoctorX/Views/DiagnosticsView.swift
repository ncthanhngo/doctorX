import SwiftUI

/// Màn "Chẩn đoán ổ" — các tính năng IT chỉ-đọc: SMART health, phát hiện mã hoá,
/// và nhật ký thao tác. Không phá dữ liệu nên không có bước xác nhận.
struct DiagnosticsView: View {
    let disks: [Disk]
    @State private var ctrl = DiagnosticsController()
    @State private var selectedBSD: String?
    @Environment(\.dismiss) private var dismiss

    private var selectedDisk: Disk? {
        disks.first { $0.bsd == selectedBSD }
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    diskPicker
                    if let disk = selectedDisk {
                        healthCard
                        encryptionCard(disk)
                    }
                    historyCard
                }
                .padding(20)
            }
        }
        .frame(width: 520, height: 640)
        .task { await ctrl.loadHistory() }
    }

    private var header: some View {
        HStack(spacing: 10) {
            Image(systemName: "stethoscope.circle").font(.title2).foregroundStyle(Brand.primary)
            VStack(alignment: .leading, spacing: 1) {
                Text("Chẩn đoán ổ").font(.headline)
                Text("SMART · Mã hoá · Nhật ký").font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            Button("Đóng") { dismiss() }.keyboardShortcut(.cancelAction)
        }
        .padding(.horizontal, 18).padding(.vertical, 12)
    }

    private var diskPicker: some View {
        SectionCard(title: "Chọn ổ", systemImage: "externaldrive") {
            if disks.isEmpty {
                Text("Chưa có ổ gắn ngoài nào.").font(.callout).foregroundStyle(.secondary)
            } else {
                ForEach(disks) { disk in
                    Button {
                        selectedBSD = disk.bsd
                        Task {
                            await ctrl.loadHealth(bsd: disk.bsd)
                            for p in disk.partitions where !p.isSystem {
                                await ctrl.detectEncryption(bsd: p.bsd)
                            }
                        }
                    } label: {
                        HStack {
                            Image(systemName: "externaldrive.fill")
                                .foregroundStyle(selectedBSD == disk.bsd ? .white : Brand.primary)
                            Text(disk.friendlyName).fontWeight(.medium).lineLimit(1)
                            Spacer()
                            Text(formatBytes(disk.sizeBytes)).font(.caption2)
                                .foregroundStyle(selectedBSD == disk.bsd ? .white.opacity(0.85) : .secondary)
                        }
                        .padding(9)
                        .background {
                            RoundedRectangle(cornerRadius: 9)
                                .fill(selectedBSD == disk.bsd ? AnyShapeStyle(Brand.heroGradient) : AnyShapeStyle(Color(nsColor: .controlBackgroundColor)))
                        }
                        .foregroundStyle(selectedBSD == disk.bsd ? .white : .primary)
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }

    // MARK: SMART

    @ViewBuilder private var healthCard: some View {
        SectionCard(title: "Sức khoẻ ổ (SMART)", systemImage: "waveform.path.ecg") {
            if ctrl.healthLoading {
                ProgressView().controlSize(.small)
            } else if let err = ctrl.healthError {
                Label(err, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(Brand.warn)
            } else if let h = ctrl.health {
                if !h.available {
                    Text(h.note.isEmpty ? "Ổ không báo SMART (thường gặp với USB qua bộ chuyển)." : h.note)
                        .font(.callout).foregroundStyle(.secondary)
                } else {
                    HStack(spacing: 8) {
                        Image(systemName: h.passed ? "checkmark.seal.fill" : "xmark.seal.fill")
                            .foregroundStyle(h.passed ? Brand.safe : Brand.warn)
                        Text(h.passed ? "Ổ báo BÌNH THƯỜNG" : "Ổ báo CÓ VẤN ĐỀ").fontWeight(.semibold)
                    }
                    healthRow("Nhiệt độ", h.temperatureC > 0 ? "\(h.temperatureC)°C" : "—")
                    healthRow("Giờ hoạt động", h.powerOnHours > 0 ? "\(h.powerOnHours) h" : "—")
                    healthRow("Sector ánh xạ lại", "\(h.reallocatedSectors)")
                    if !h.note.isEmpty {
                        Text(h.note).font(.caption).foregroundStyle(Brand.warn)
                    }
                }
            } else {
                Text("Chọn một ổ để đọc SMART.").font(.callout).foregroundStyle(.secondary)
            }
        }
    }

    private func healthRow(_ k: String, _ v: String) -> some View {
        HStack {
            Text(k).foregroundStyle(.secondary)
            Spacer()
            Text(v).fontWeight(.medium)
        }
        .font(.callout)
    }

    // MARK: Mã hoá

    private func encryptionCard(_ disk: Disk) -> some View {
        SectionCard(title: "Mã hoá phân vùng", systemImage: "lock.shield") {
            let parts = disk.partitions.filter { !$0.isSystem }
            if parts.isEmpty {
                Text("Không có phân vùng dữ liệu.").font(.callout).foregroundStyle(.secondary)
            } else {
                ForEach(parts) { p in
                    HStack {
                        Text(p.displayName).lineLimit(1)
                        Spacer()
                        encryptionBadge(ctrl.encryption[p.bsd])
                    }
                    .font(.callout)
                }
            }
        }
    }

    @ViewBuilder private func encryptionBadge(_ kind: String?) -> some View {
        switch kind {
        case "bitlocker":
            badge("BitLocker", "lock.fill", Brand.warn)
        case "filevault":
            badge("FileVault", "lock.fill", Brand.warn)
        case "encrypted":
            badge("Đã mã hoá", "lock.fill", Brand.warn)
        case "none":
            badge("Không mã hoá", "lock.open", .secondary)
        default:
            Text("—").foregroundStyle(.tertiary)
        }
    }

    private func badge(_ text: String, _ icon: String, _ tint: Color) -> some View {
        Label(text, systemImage: icon)
            .font(.caption.weight(.medium))
            .padding(.horizontal, 8).padding(.vertical, 3)
            .background(tint.opacity(0.15), in: Capsule())
            .foregroundStyle(tint)
    }

    // MARK: Nhật ký

    private var historyCard: some View {
        SectionCard(title: "Nhật ký thao tác", systemImage: "clock.arrow.circlepath") {
            if ctrl.history.isEmpty {
                Text("Chưa có thao tác nào được ghi lại.").font(.callout).foregroundStyle(.secondary)
            } else {
                ForEach(ctrl.history.prefix(50)) { row in
                    HStack(alignment: .top, spacing: 8) {
                        Image(systemName: row.result == "ok" ? "checkmark.circle.fill" : "xmark.circle.fill")
                            .foregroundStyle(row.result == "ok" ? Brand.safe : Brand.warn)
                            .font(.caption)
                        VStack(alignment: .leading, spacing: 1) {
                            Text("\(opLabel(row.op)) · \(row.device)").font(.callout.weight(.medium))
                            if !row.detail.isEmpty {
                                Text(row.detail).font(.caption2).foregroundStyle(.secondary).lineLimit(2)
                            }
                            Text(row.time).font(.caption2).foregroundStyle(.tertiary)
                        }
                        Spacer(minLength: 0)
                    }
                    .padding(.vertical, 2)
                }
            }
        }
    }

    private func opLabel(_ op: String) -> String {
        switch op {
        case "flash": return "Ghi image"
        case "format": return "Format"
        case "wipe": return "Xoá an toàn"
        case "capture": return "Bắt ảnh"
        case "bad_blocks": return "Kiểm tra ổ"
        default: return op
        }
    }
}
