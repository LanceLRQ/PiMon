package plugins

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/LanceLRQ/PiMon/src/pkg/plugin/manifest"
)

// MachineManifest 是某台机器（agent 设备）上报的一个可用插件。Manifest 须已经过 manifest.FromJSON 校验。
type MachineManifest struct {
	Origin   Origin
	Manifest *manifest.Manifest
}

// machineCache 是一台机器的可用插件缓存。
type machineCache struct {
	plugins []Plugin
	byID    map[string]Plugin
}

// SyncMachine 把 machine（设备 ID）上报的可用插件 manifest 入库，语义同本机扫描：
// 集合内的 upsert 为可用，该机器其余可用记录标为不可用。随后使该机器的缓存失效。
// machine 不能是 hub；来源只接受 builtin、exec；同一插件只能出现一次。
func (r *Registry) SyncMachine(ctx context.Context, machine string, ms []MachineManifest) error {
	if machine == "" || machine == MachineHub {
		return fmt.Errorf("SyncMachine 不能写入机器 %q", machine)
	}
	seen := make(map[string]bool, len(ms))
	for _, m := range ms {
		if m.Manifest == nil {
			return errors.New("manifest 为空")
		}
		if m.Origin != OriginBuiltin && m.Origin != OriginExec {
			return fmt.Errorf("插件 %s 的来源 %q 不合法", m.Manifest.ID, m.Origin)
		}
		if seen[m.Manifest.ID] {
			return fmt.Errorf("插件 %s 重复", m.Manifest.ID)
		}
		seen[m.Manifest.ID] = true
	}
	err := r.syncRows(ctx, machine, ms)
	r.invalidate(machine)
	return err
}

// DropMachine 删除 machine 的全部 manifest 记录并丢弃其缓存（设备吊销或被重新配对替换后调用）。
// devices 仓库删除设备时已在事务内删掉这些行，这里兜底清掉与吊销并发写入的记录。
func (r *Registry) DropMachine(ctx context.Context, machine string) error {
	if machine == "" || machine == MachineHub {
		return fmt.Errorf("DropMachine 不能删除机器 %q", machine)
	}
	var err error
	if r.cfg.DB != nil {
		_, err = r.cfg.DB.ExecContext(ctx, `DELETE FROM manifests WHERE machine = ?`, machine)
	}
	r.invalidate(machine)
	return err
}

// invalidate 丢弃 machine 的缓存并递增失效代数，使正在读库的旧结果不会写回缓存。
func (r *Registry) invalidate(machine string) {
	r.machMu.Lock()
	defer r.machMu.Unlock()
	delete(r.machCache, machine)
	if r.machGen == nil {
		r.machGen = map[string]uint64{}
	}
	r.machGen[machine]++
}

// MachinePlugins 返回 machine 上可用的插件（按 id 升序）。hub 取内存快照；agent 设备读 manifests 表
// 中 available = 1 的记录（带缓存），这些插件只有 ID、Origin、Manifest。
func (r *Registry) MachinePlugins(ctx context.Context, machine string) ([]Plugin, error) {
	if machine == MachineHub {
		return r.Snapshot().Plugins, nil
	}
	c, err := r.machine(ctx, machine)
	if err != nil {
		return nil, err
	}
	return append([]Plugin(nil), c.plugins...), nil
}

// MachinePlugin 按 id 取 machine 上可用的插件。hub 等同 Get；读库失败按不可用处理。
func (r *Registry) MachinePlugin(machine, id string) (Plugin, bool) {
	if machine == MachineHub {
		return r.Get(id)
	}
	c, err := r.machine(context.Background(), machine)
	if err != nil {
		return Plugin{}, false
	}
	p, ok := c.byID[id]
	return p, ok
}

// machine 取缓存，未命中时读库并在期间没有失效的前提下写入缓存。
func (r *Registry) machine(ctx context.Context, machine string) (*machineCache, error) {
	r.machMu.Lock()
	if c, ok := r.machCache[machine]; ok {
		r.machMu.Unlock()
		return c, nil
	}
	gen := r.machGen[machine]
	r.machMu.Unlock()

	c, err := r.loadMachine(ctx, machine)
	if err != nil {
		return nil, err
	}
	r.machMu.Lock()
	if r.machGen[machine] == gen {
		if r.machCache == nil {
			r.machCache = map[string]*machineCache{}
		}
		r.machCache[machine] = c
	}
	r.machMu.Unlock()
	return c, nil
}

// loadMachine 读 machine 的可用 manifest；库里的记录在入库前已校验，读回时用 manifest.FromJSON
// 重新解码并预编译 pattern，个别记录无法解码时跳过。
func (r *Registry) loadMachine(ctx context.Context, machine string) (*machineCache, error) {
	c := &machineCache{byID: map[string]Plugin{}}
	if r.cfg.DB == nil {
		return c, nil
	}
	rows, err := r.cfg.DB.QueryContext(ctx,
		`SELECT plugin_id, origin, manifest_json FROM manifests WHERE machine = ? AND available = 1`, machine)
	if err != nil {
		return nil, fmt.Errorf("查询机器 %s 的 manifest: %w", machine, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, origin, raw string
		if err := rows.Scan(&id, &origin, &raw); err != nil {
			return nil, err
		}
		m, err := manifest.FromJSON([]byte(raw))
		if err != nil || m.ID != id {
			continue
		}
		p := Plugin{ID: id, Origin: Origin(origin), Manifest: m}
		c.plugins = append(c.plugins, p)
		c.byID[id] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(c.plugins, func(i, j int) bool { return c.plugins[i].ID < c.plugins[j].ID })
	return c, nil
}
