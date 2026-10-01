package instances

import (
	"context"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
	"github.com/LanceLRQ/PiMon/src/pkg/plugin/report"
)

// ScreenData 返回屏幕端渲染一个小组件所需的实例数据：展示状态与最新报告的数据项。
// 只含展示信息，不含配置、事件与插件私有状态；不存在返回 ErrNotFound。
func (s *Service) ScreenData(ctx context.Context, id string) (model.ScreenInstanceData, error) {
	r, err := s.getRow(ctx, id)
	if err != nil {
		return model.ScreenInstanceData{}, err
	}
	in := s.toInstance(r)
	items := []report.Item{}
	s.mu.Lock()
	if st := s.states[id]; st != nil && st.report != nil {
		items = append(items, st.report.Items...)
	}
	s.mu.Unlock()
	return model.ScreenInstanceData{
		InstanceID: in.ID, DisplayState: in.DisplayState, ReportStatus: in.ReportStatus,
		ReportStale: in.ReportStale, Summary: in.Summary, LastSuccessAt: in.LastSuccessAt, Items: items,
	}, nil
}
