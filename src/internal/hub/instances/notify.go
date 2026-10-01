package instances

import (
	"context"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// OnChange 注册实例变化的订阅回调：实例新建、修改、复制、暂停、恢复、删除，
// 以及采集结果写入当前状态或重新排程之后，以实例 id 调用。
// 回调在业务路径上同步执行，必须非阻塞、不得回调本服务的会改变实例的方法；
// 删除时 id 对应的实例已不存在。重复注册会覆盖前一个。
func (s *Service) OnChange(f func(id string)) {
	s.cbMu.Lock()
	s.onChange = f
	s.cbMu.Unlock()
}

// notify 通知订阅者该实例已变化。调用时不得持有 s.mu。
func (s *Service) notify(id string) {
	s.cbMu.RLock()
	f := s.onChange
	s.cbMu.RUnlock()
	if f != nil {
		f(id)
	}
}

// View 返回单个实例的列表视图（与 List 的元素同形）；不存在返回 ErrNotFound。
func (s *Service) View(ctx context.Context, id string) (model.Instance, error) {
	r, err := s.getRow(ctx, id)
	if err != nil {
		return model.Instance{}, err
	}
	return s.toInstance(r), nil
}
