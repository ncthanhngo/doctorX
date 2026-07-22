import SwiftUI

struct ContentView: View {
    @State private var state = AppState()
    @State private var showQuickRestore = false
    @State private var showCreateUSB = false
    @State private var deviceMonitor = DeviceMonitor()

    var body: some View {
        NavigationSplitView {
            SidebarView(state: state)
                .navigationSplitViewColumnWidth(min: 260, ideal: 300)
        } detail: {
            Group {
                if let part = state.selected {
                    VolumeDetailView(state: state, partition: part, showQuickRestore: $showQuickRestore)
                } else {
                    WelcomeView(reachable: state.isDaemonReachable, hasDisks: !state.disks.isEmpty)
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(WindowBackground())
        }
        .task {
            await state.refreshDevices()
            deviceMonitor.start { Task { await state.refreshDevices() } }
            // Lưới an toàn: poll chậm phòng khi DiskArbitration bỏ sót sự kiện.
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(30))
                await state.refreshDevices()
            }
        }
        .onDisappear { deviceMonitor.stop() }
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button {
                    showCreateUSB = true
                } label: {
                    Label("Tạo USB", systemImage: "externaldrive.badge.plus")
                }
                .help("Ghi image, format hoặc kiểm tra ổ (xoá dữ liệu)")
            }
        }
        .sheet(isPresented: $showQuickRestore) { QuickRestoreView(state: state) }
        .sheet(isPresented: $showCreateUSB) { CreateUSBView(disks: state.disks) }
        .alert("Có lỗi xảy ra", isPresented: .init(
            get: { state.errorMessage != nil },
            set: { if !$0 { state.errorMessage = nil } }
        )) {
            Button("Đóng", role: .cancel) { state.errorMessage = nil }
        } message: {
            Text(state.errorMessage ?? "")
        }
        .overlay { if state.isBusy { BusyOverlay(message: state.busyMessage) } }
    }
}

/// Nền cửa sổ có gợn màu nhẹ theo thương hiệu.
struct WindowBackground: View {
    var body: some View {
        LinearGradient(
            colors: [Brand.primary.opacity(0.05), Color.clear],
            startPoint: .topLeading, endPoint: .bottomTrailing
        )
        .ignoresSafeArea()
    }
}

// MARK: - Sidebar

struct SidebarView: View {
    let state: AppState

    var body: some View {
        VStack(spacing: 0) {
            BrandHeader()

            if state.disks.isEmpty {
                SidebarEmpty()
            } else {
                ScrollView {
                    VStack(spacing: 10) {
                        ForEach(state.disks) { disk in
                            DiskGroup(disk: disk, state: state)
                        }
                    }
                    .padding(12)
                }
            }
            Spacer(minLength: 0)
            SidebarFooter(reachable: state.isDaemonReachable)
        }
        .background(Color(nsColor: .windowBackgroundColor))
    }
}

/// Đầu trang thương hiệu với logo chữ thập y tế.
struct BrandHeader: View {
    var body: some View {
        HStack(spacing: 10) {
            ZStack {
                RoundedRectangle(cornerRadius: 9).fill(Brand.heroGradient)
                Image(systemName: "cross.case.fill").foregroundStyle(.white).font(.system(size: 16, weight: .bold))
            }
            .frame(width: 34, height: 34)
            VStack(alignment: .leading, spacing: 0) {
                Text("DoctorX").font(.headline.bold())
                Text("Cứu dữ liệu USB & ổ ngoài").font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
        }
        .padding(.horizontal, 14).padding(.vertical, 12)
        .background(.ultraThinMaterial)
        .overlay(Divider(), alignment: .bottom)
    }
}

struct DiskGroup: View {
    let disk: Disk
    let state: AppState

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 5) {
                Image(systemName: "externaldrive.fill").font(.caption).foregroundStyle(.secondary)
                Text(disk.model).font(.caption.weight(.medium)).foregroundStyle(.secondary)
                    .lineLimit(1)
                Spacer()
                Text(formatBytes(disk.sizeBytes)).font(.caption2).foregroundStyle(.tertiary)
            }
            ForEach(disk.partitions) { part in
                PartitionCard(
                    partition: part,
                    diskSize: disk.sizeBytes,
                    isSelected: state.selected?.bsd == part.bsd,
                    onTap: { if !part.isSystem { state.select(part) } }
                )
            }
        }
    }
}

/// Thẻ phân vùng — trực quan, bấm để chọn.
struct PartitionCard: View {
    let partition: Partition
    let diskSize: Int64
    let isSelected: Bool
    let onTap: () -> Void

    private var fraction: Double {
        diskSize > 0 ? Double(partition.sizeBytes) / Double(diskSize) : 0
    }

    var body: some View {
        Button(action: onTap) {
            HStack(spacing: 11) {
                ZStack {
                    RoundedRectangle(cornerRadius: 9)
                        .fill(partition.isSystem ? Color.secondary.opacity(0.15) : Brand.primary.opacity(0.15))
                    Image(systemName: partition.isSystem ? "lock.fill" : "internaldrive.fill")
                        .foregroundStyle(partition.isSystem ? Color.secondary : Brand.primary)
                }
                .frame(width: 38, height: 38)

                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 6) {
                        Text(partition.displayName).fontWeight(.semibold).lineLimit(1)
                        FSBadge(fs: partition.fsType)
                    }
                    if partition.isSystem {
                        Text("Phân vùng hệ thống").font(.caption2).foregroundStyle(.secondary)
                    } else {
                        CapacityBar(usedFraction: fraction, tint: isSelected ? .white : Brand.primary)
                        Text(formatBytes(partition.sizeBytes)).font(.caption2)
                            .foregroundStyle(isSelected ? .white.opacity(0.85) : .secondary)
                    }
                }
                Spacer(minLength: 0)
            }
            .padding(10)
            .background {
                RoundedRectangle(cornerRadius: 12)
                    .fill(isSelected ? AnyShapeStyle(Brand.heroGradient) : AnyShapeStyle(Color(nsColor: .controlBackgroundColor)))
            }
            .overlay(RoundedRectangle(cornerRadius: 12).stroke(.primary.opacity(isSelected ? 0 : 0.07)))
            .foregroundStyle(isSelected ? .white : .primary)
        }
        .buttonStyle(.plain)
        .disabled(partition.isSystem)
        .opacity(partition.isSystem ? 0.7 : 1)
        .help(partition.isSystem ? "Phân vùng hệ thống — DoctorX không thao tác lên đây." : "")
    }
}

struct SidebarEmpty: View {
    @State private var pulse = false

    var body: some View {
        VStack(spacing: 14) {
            Spacer()
            ZStack {
                Circle().fill(Brand.primary.opacity(0.10))
                    .frame(width: 92, height: 92)
                    .scaleEffect(pulse ? 1.15 : 0.9)
                    .opacity(pulse ? 0 : 0.8)
                Circle().fill(Brand.primary.opacity(0.14)).frame(width: 66, height: 66)
                Image(systemName: "arrow.down.to.line").font(.system(size: 26, weight: .semibold))
                    .foregroundStyle(Brand.primary)
            }
            Text("Đang chờ ổ đĩa").font(.headline)
            Text("Cắm USB hoặc ổ cứng gắn ngoài — DoctorX tự nhận ngay.")
                .font(.callout).foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 24)
            Spacer()
        }
        .frame(maxWidth: .infinity)
        .onAppear {
            withAnimation(.easeOut(duration: 1.6).repeatForever(autoreverses: false)) { pulse = true }
        }
    }
}

struct SidebarFooter: View {
    let reachable: Bool
    var body: some View {
        HStack(spacing: 6) {
            Circle().fill(reachable ? Brand.safe : Brand.warn).frame(width: 8, height: 8)
            Text(reachable ? "Dịch vụ nền đang chạy" : "Chưa kết nối dịch vụ nền")
                .font(.caption).foregroundStyle(.secondary)
            Spacer()
        }
        .padding(.horizontal, 14).padding(.vertical, 9)
        .overlay(Divider(), alignment: .top)
    }
}

// MARK: - Overlay bận

struct BusyOverlay: View {
    let message: String
    var body: some View {
        ZStack {
            Color.black.opacity(0.28).ignoresSafeArea()
            VStack(spacing: 14) {
                ProgressView().controlSize(.large)
                if !message.isEmpty {
                    Text(message).font(.callout.weight(.medium))
                }
            }
            .padding(28)
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 16))
            .shadow(radius: 20, y: 8)
        }
    }
}
