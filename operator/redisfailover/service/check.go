package service

import (
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"

	redisfailoverv1alpha2 "github.com/spotahome/redis-operator/api/redisfailover/v1alpha2"
	"github.com/spotahome/redis-operator/log"
	"github.com/spotahome/redis-operator/service/k8s"
	"github.com/spotahome/redis-operator/service/redis"
)

// RedisFailoverCheck defines the interface able to check the correct status of a redis failover
type RedisFailoverCheck interface {
	CheckRedisNumber(rFailover *redisfailoverv1alpha2.RedisFailover) error
	CheckSentinelNumber(rFailover *redisfailoverv1alpha2.RedisFailover) error
	CheckAllSlavesFromMaster(master string, rFailover *redisfailoverv1alpha2.RedisFailover) error
	CheckSentinelNumberInMemory(sentinel string, rFailover *redisfailoverv1alpha2.RedisFailover) error
	CheckSentinelSlavesNumberInMemory(sentinel string, rFailover *redisfailoverv1alpha2.RedisFailover) error
	CheckSentinelMonitor(sentinel,password string, monitor string) error
	GetMasterIP(rFailover *redisfailoverv1alpha2.RedisFailover) (string, error)
	GetNumberMasters(rFailover *redisfailoverv1alpha2.RedisFailover) (int, error)
	GetRedisesIPs(rFailover *redisfailoverv1alpha2.RedisFailover) ([]string, error)
	GetSentinelsIPs(rFailover *redisfailoverv1alpha2.RedisFailover) ([]string, error)
	GetMinimumRedisPodTime(rFailover *redisfailoverv1alpha2.RedisFailover) (time.Duration, error)
}

// RedisFailoverChecker is our implementation of RedisFailoverCheck interface
type RedisFailoverChecker struct {
	k8sService  k8s.Services
	redisClient redis.Client
	logger      log.Logger
}

// NewRedisFailoverChecker creates an object of the RedisFailoverChecker struct
func NewRedisFailoverChecker(k8sService k8s.Services, redisClient redis.Client, logger log.Logger) *RedisFailoverChecker {
	return &RedisFailoverChecker{
		k8sService:  k8sService,
		redisClient: redisClient,
		logger:      logger,
	}
}

// CheckRedisNumber controlls that the number of deployed redis is the same than the requested on the spec
func (r *RedisFailoverChecker) CheckRedisNumber(rf *redisfailoverv1alpha2.RedisFailover) error {
	ss, err := r.k8sService.GetStatefulSet(rf.Namespace, GetRedisName(rf))
	if err != nil {
		return err
	}
	if rf.Spec.Redis.Replicas != *ss.Spec.Replicas {
		return errors.New("number of redis pods differ from specification")
	}
	return nil
}

// CheckSentinelNumber controlls that the number of deployed sentinel is the same than the requested on the spec
func (r *RedisFailoverChecker) CheckSentinelNumber(rf *redisfailoverv1alpha2.RedisFailover) error {
	d, err := r.k8sService.GetDeployment(rf.Namespace, GetSentinelName(rf))
	if err != nil {
		return err
	}
	if rf.Spec.Sentinel.Replicas != *d.Spec.Replicas {
		return errors.New("number of sentinel pods differ from specification")
	}
	return nil
}

// CheckAllSlavesFromMaster controlls that all slaves have the same master (the real one)
func (r *RedisFailoverChecker) CheckAllSlavesFromMaster(master string, rf *redisfailoverv1alpha2.RedisFailover) error {
	rips, err := r.GetRedisesIPs(rf)
	if err != nil {
		return err
	}
	for _, rip := range rips {
		slave, err := r.redisClient.GetSlaveOf(rip,rf.Spec.Password)
		if err != nil {
			return err
		}
		if slave != "" && slave != master {
			return fmt.Errorf("slave %s don't have the master %s, has %s", rip, master, slave)
		}
	}
	return nil
}

// CheckSentinelNumberInMemory controls that sentinels have only the living sentinels on its memory.
func (r *RedisFailoverChecker) CheckSentinelNumberInMemory(sentinel string, rf *redisfailoverv1alpha2.RedisFailover) error {
	sips, err := r.GetSentinelsIPs(rf)
	if err != nil {
		return err
	}
	for _, sip := range sips {
		nSentinels, err := r.redisClient.GetNumberSentinelsInMemory(sip,rf.Spec.Password)
		if err != nil {
			return err
		} else if nSentinels != rf.Spec.Sentinel.Replicas {
			return errors.New("sentinels in memory mismatch")
		}
	}
	return nil
}

// CheckSentinelSlavesNumberInMemory controls that sentinels have only the spected slaves number.
func (r *RedisFailoverChecker) CheckSentinelSlavesNumberInMemory(sentinel string, rf *redisfailoverv1alpha2.RedisFailover) error {
	sips, err := r.GetSentinelsIPs(rf)
	if err != nil {
		return err
	}
	for _, sip := range sips {
		nSlaves, err := r.redisClient.GetNumberSentinelSlavesInMemory(sip,rf.Spec.Password)
		if err != nil {
			return err
		} else if nSlaves != rf.Spec.Sentinel.Replicas-1 {
			return errors.New("sentinels in memory mismatch")
		}
	}
	return nil
}

// CheckSentinelMonitor controls if the sentinels are monitoring the expected master
func (r *RedisFailoverChecker) CheckSentinelMonitor(sentinel,password string, monitor string) error {
	actualMonitorIP, err := r.redisClient.GetSentinelMonitor(sentinel,password)
	if err != nil {
		return err
	}
	if actualMonitorIP != monitor {
		return errors.New("the monitor on the sentinel config does not match with the expected one")
	}
	return nil
}

// GetMasterIP connects to all redis and returns the master of the redis failover
func (r *RedisFailoverChecker) GetMasterIP(rf *redisfailoverv1alpha2.RedisFailover) (string, error) {
	rips, err := r.GetRedisesIPs(rf)
	if err != nil {
		return "", err
	}
	masters := []string{}
	for _, rip := range rips {
		master, err := r.redisClient.IsMaster(rip,rf.Spec.Password)
		if err != nil {
			return "", err
		}
		if master {
			masters = append(masters, rip)
		}
	}

	if len(masters) != 1 {
		return "", errors.New("number of redis nodes known as master is different than 1")
	}
	return masters[0], nil
}

// GetNumberMasters returns the number of redis nodes that are working as a master
func (r *RedisFailoverChecker) GetNumberMasters(rf *redisfailoverv1alpha2.RedisFailover) (int, error) {
	nMasters := 0
	rips, err := r.GetRedisesIPs(rf)
	if err != nil {
		return nMasters, err
	}
	for _, rip := range rips {
		master, err := r.redisClient.IsMaster(rip,rf.Spec.Password)
		if err != nil {
			return nMasters, err
		}
		if master {
			nMasters++
		}
	}
	return nMasters, nil
}

// GetRedisesIPs returns the IPs of the Redis nodes
func (r *RedisFailoverChecker) GetRedisesIPs(rf *redisfailoverv1alpha2.RedisFailover) ([]string, error) {
	redises := []string{}
	rps, err := r.k8sService.GetStatefulSetPods(rf.Namespace, GetRedisName(rf))
	if err != nil {
		return nil, err
	}
	for _, rp := range rps.Items {
		if rp.Status.Phase == corev1.PodRunning { // Only work with running pods
			redises = append(redises, rp.Status.PodIP)
		}
	}
	return redises, nil
}

// GetSentinelsIPs returns the IPs of the Sentinel nodes
func (r *RedisFailoverChecker) GetSentinelsIPs(rf *redisfailoverv1alpha2.RedisFailover) ([]string, error) {
	sentinels := []string{}
	rps, err := r.k8sService.GetDeploymentPods(rf.Namespace, GetSentinelName(rf))
	if err != nil {
		return nil, err
	}
	for _, sp := range rps.Items {
		if sp.Status.Phase == corev1.PodRunning { // Only work with running pods
			sentinels = append(sentinels, sp.Status.PodIP)
		}
	}
	return sentinels, nil
}

// GetMinimumRedisPodTime returns the minimum time a pod is alive
func (r *RedisFailoverChecker) GetMinimumRedisPodTime(rf *redisfailoverv1alpha2.RedisFailover) (time.Duration, error) {
	minTime := 100000 * time.Hour // More than ten years
	rps, err := r.k8sService.GetStatefulSetPods(rf.Namespace, GetRedisName(rf))
	if err != nil {
		return minTime, err
	}
	for _, redisNode := range rps.Items {
		start := redisNode.Status.StartTime.Round(time.Second)
		alive := time.Now().Sub(start)
		r.logger.Debugf("Pod %s has been alive for %.f seconds", redisNode.Status.PodIP, alive.Seconds())
		if alive < minTime {
			minTime = alive
		}
	}
	return minTime, nil
}
