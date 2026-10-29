package node

import (
	"context"
	"errors"

	log "github.com/sirupsen/logrus"
	panel "github.com/wyx2685/v2node/api/v2board"
)

func (c *Controller) reportUserTrafficTask(ctx context.Context) (err error) {
	var reportmin = 0
	var devicemin = 0
	if c.info.Common.BaseConfig != nil {
		reportmin = c.info.Common.BaseConfig.NodeReportMinTraffic
		devicemin = c.info.Common.BaseConfig.DeviceOnlineMinTraffic
	}
	userTraffic, reportErr := c.flushTraffic(ctx, reportmin, false)
	if reportErr != nil {
		log.WithFields(log.Fields{"tag": c.tag, "err": reportErr}).Info("Report user traffic failed; pending managed traffic retained")
		if errors.Is(reportErr, context.Canceled) || errors.Is(reportErr, context.DeadlineExceeded) {
			return reportErr
		}
	} else if len(userTraffic) > 0 {
		log.WithField("tag", c.tag).Infof("Report %d users traffic", len(userTraffic))
	}

	if onlineDevice, err := c.limiter.GetOnlineDevice(); err != nil {
		log.WithFields(log.Fields{
			"tag": c.tag,
			"err": err,
		}).Info("Get online device failed")
	} else if len(*onlineDevice) > 0 {
		var result []panel.OnlineUser
		var nocountUID = make(map[int]struct{})
		for _, traffic := range userTraffic {
			total := traffic.Upload + traffic.Download
			if total < int64(devicemin*1000) {
				nocountUID[traffic.UID] = struct{}{}
			}
		}
		for _, online := range *onlineDevice {
			if _, ok := nocountUID[online.UID]; !ok {
				result = append(result, online)
			}
		}
		data := make(map[int][]string)
		for _, onlineuser := range result {
			// json structure: { UID1:["ip1","ip2"],UID2:["ip3","ip4"] }
			data[onlineuser.UID] = append(data[onlineuser.UID], onlineuser.IP)
		}
		if len(data) != 0 {
			err := c.apiClient.ReportNodeOnlineUsers(ctx, &data)
			if err != nil {
				log.WithFields(log.Fields{
					"tag": c.tag,
					"err": err,
				}).Info("Report online users failed")
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return err
				}
			}
		}
		log.WithField("tag", c.tag).Infof("Total %d online users, %d Reported", len(*onlineDevice), len(result))
	}

	return nil
}

// Normal reporting drains outstanding immutable batches before resetting more
// counters. Shutdown additionally persists the remaining below-threshold delta,
// even when a previous batch is still awaiting acknowledgement.
func (c *Controller) flushTraffic(ctx context.Context, minimum int, final bool) ([]panel.UserTraffic, error) {
	send := func(ctx context.Context, traffic []panel.UserTraffic, id string) error {
		return c.apiClient.ReportUserTraffic(ctx, traffic, id)
	}
	if c.trafficQueue != nil && !final {
		if err := c.trafficQueue.Drain(ctx, send); err != nil {
			return nil, err
		}
	}
	traffic, err := c.server.GetUserTrafficSlice(c.tag, minimum)
	if err != nil {
		return nil, err
	}
	if c.trafficQueue != nil {
		if err := c.trafficQueue.Append(traffic); err != nil {
			return traffic, err
		}
		return traffic, c.trafficQueue.Drain(ctx, send)
	}
	if len(traffic) > 0 {
		return traffic, c.apiClient.ReportUserTraffic(ctx, traffic)
	}
	return traffic, nil
}

func compareUserList(old, new []panel.UserInfo) (deleted, added, modified []panel.UserInfo) {
	oldMap := make(map[string]panel.UserInfo, len(old))
	for _, u := range old {
		oldMap[u.Uuid] = u
	}

	for _, u := range new {
		if o, ok := oldMap[u.Uuid]; !ok {
			added = append(added, u)
		} else {
			if o.SpeedLimit != u.SpeedLimit || o.DeviceLimit != u.DeviceLimit {
				modified = append(modified, u)
			}
			delete(oldMap, u.Uuid)
		}
	}

	for _, o := range oldMap {
		deleted = append(deleted, o)
	}

	return deleted, added, modified
}
