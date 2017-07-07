package metrics

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/fsouza/go-dockerclient"
	info "github.com/google/cadvisor/info/v1"
	"github.com/jmoiron/jsonq"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	CONTAINER_INDEX_PREFIX = "container_"

	THRESHOLD_LOW_SUFFIX  = "_low_threshold"
	THRESHOLD_HIGH_SUFFIX = "_high_threshold"

	THRESHOLD_CAL_RESULT_SUFFIX = "_result"

	LINKER_SG_ID            = "LINKER_SERVICE_GROUP_ID"
	LINKER_SGI_ID           = "LINKER_SERVICE_GROUP_INSTANCE_ID"
	LINKDER_SO_ID           = "LINKER_SERVICE_ORDER_ID"
	LINKER_APP_CONTAINER_ID = "LINKER_APP_CONTAINER_ID"
	LINKER_MESOS_TASK_ID    = "MESOS_TASK_ID"
	//	LINKER_WEAVE_CIDR = "WEAVE_CIDR"
	LINKER_WEAVE_CIDR         = "HOST"
	LINKER_GROUP_ID           = "LINKER_GROUP_ID"
	LINKER_APP_ID             = "LINKER_APP_ID"
	LINKER_REPAIR_TEMPALTE_ID = "LINKER_REPAIR_TEMPLATE_ID"

	INDEX_MEMORY_USAGE                    = "memory_usage"
	INDEX_CPU_USAGE                       = "cpu_usage"
	INDEX_LOAD_USAGE                      = "load_usage"
	INDEX_NETWORK_TRANSMIT_PACKAGES_TOTAL = "network_transmit_packets_per_second_total"
	INDEX_NETWORK_RECEIVE_PACKAGES_TOTAL  = "network_receive_packets_per_second_total"
	INDEX_NETWORK_TRANSMIT_PACKAGE_NUMBER = "transmit_package_number"
	INDEX_GW_CONNECTIONS                  = "gw_conn_number"
	INDEX_PGW_CONNECTIONS                 = "pgw_conn_number"
	INDEX_SGW_CONNECTIONS                 = "sgw_conn_number"

	ALERT_NAME = "alert_name"

	ALERT_ENABLE = "ALERT_ENABLE"

	ALERT_HIGH_MEMORY                  = "HighMemoryAlert"
	ALERT_LOW_MEMORY                   = "LowMemoryAlert"
	ALERT_HIGH_CPU                     = "HighCpuAlert"
	ALERT_LOW_CPU                      = "LowCpuAlert"
	ALERT_HIGH_REV_PACKAGES            = "HighRevPackagesAlert"
	ALERT_LOW_REV_PACKAGES             = "LowRevPackagesAlert"
	ALERT_HIGH_TRANSMIT_PACKAGE_NUMBER = "HighTransmitPackageNumberAlert"
	ALERT_LOW_TRANSMIT_PACKAGE_NUMBER  = "LowTransmitPackageNumberAlert"

	ALERT_HIGH_CURRENT_SESSION = "HighCurrentSessionAlert"
	ALERT_LOW_CURRENT_SESSION  = "LowCurrentSessionAlert"

	//PGW & SGW Instances alerts
	ALERT_PGW_HIGH_CONNECTIONS = "HighPgwConnectionsAlert"
	ALERT_PGW_LOW_CONNECTIONS  = "LowPgwConnectionsAlert"
	ALERT_SGW_HIGH_CONNECTIONS = "HighSgwConnectionsAlert"
	ALERT_SGW_LOW_CONNECTIONS  = "LowSgwConnectionsAlert"

	INDEX_CURRENT_SESSION = "current_session"
	MIN_NODE_NUMBER       = "INSTANCE_MIN_NUM"
	MAX_NODE_NUMBER       = "INSTANCE_MAX_NUM"

	DNS_DOMAINNAME            = "marathonlb-lb-linkerdns.marathon.mesos"
	LINKER_ELASTICSERACH_PORT = "10092"
)

func (c *PrometheusCollector) CalLinkerIndexs(index, description string, container *info.ContainerInfo, ch chan<- prometheus.Metric, now, previous *info.ContainerStats, nodeNumber int64) {
	labels := []string{"id", "image", "name", "service_group_id", "service_group_instance_id", "service_order_id", "app_container_id", "mesos_task_id", "group_id", "app_id", "repair_template_id", "alert"}

	id := container.Name
	name := id
	if len(container.Aliases) > 0 {
		name = container.Aliases[0]
	}

	image := container.Spec.Image

	containerInfo, err := c.client.InspectContainer(name)

	if !c.IsAlertEnable(containerInfo) {
		return
	}

	if err != nil {
		// inspect docker instance failed.
	} else {
		serviceGroupId := GetContainerEnvValue(containerInfo, LINKER_SG_ID)
		serviceGroupInstanceId := GetContainerEnvValue(containerInfo, LINKER_SGI_ID)
		serviceOrderId := GetContainerEnvValue(containerInfo, LINKDER_SO_ID)
		appContainerId := GetContainerEnvValue(containerInfo, LINKER_APP_CONTAINER_ID)
		mesosTaskId := GetContainerEnvValue(containerInfo, LINKER_MESOS_TASK_ID)
		groupId := GetContainerEnvValue(containerInfo, LINKER_GROUP_ID)
		appId := GetContainerEnvValue(containerInfo, LINKER_APP_ID)
		repairTemplateId := GetContainerEnvValue(containerInfo, LINKER_REPAIR_TEMPALTE_ID)
		baseLabelValues := []string{id, image, name, serviceGroupId, serviceGroupInstanceId, serviceOrderId, appContainerId, mesosTaskId, groupId, appId, repairTemplateId, "true"}
		value := float64(0)

		lowThreadoldEnv := index + THRESHOLD_LOW_SUFFIX
		lowThresholdSValue := GetContainerEnvValue(containerInfo, strings.ToUpper(lowThreadoldEnv))
		highThreadoldEnv := index + THRESHOLD_HIGH_SUFFIX
		highThresholdSValue := GetContainerEnvValue(containerInfo, strings.ToUpper(highThreadoldEnv))

		fmt.Printf("high threshold is %s\n", highThresholdSValue)
		fmt.Printf("low threshold is %s\n", lowThresholdSValue)

		lowThreshold := 0.0
		// check if Low Threshold is set.
		if len(lowThresholdSValue) != 0 {
			lowThreshold, _ = strconv.ParseFloat(lowThresholdSValue, 64)
		}
		fmt.Printf("lowThreshold is %v \n", lowThreshold)

		highThreshold := 0.0
		// check if High Threshold is set.
		if len(highThresholdSValue) != 0 {
			highThreshold, _ = strconv.ParseFloat(highThresholdSValue, 64)
		}
		fmt.Printf("highThreshold is %v \n", highThreshold)

		labelSlice := labels[0:len(labels)]
		valueSlice := baseLabelValues[0:len(baseLabelValues)]

		// check index type
		switch index {
		case INDEX_CPU_USAGE:
			{
				// Maybe there are more than one network adpater here.
				if previous == nil {
					value = float64(now.Cpu.Usage.Total)
				} else {
					value = float64(now.Cpu.Usage.Total - previous.Cpu.Usage.Total)
					interval := now.Timestamp.UnixNano() - previous.Timestamp.UnixNano()
					value = value * 100.0 / float64(interval)
				}

				//				// add label low and high threshold
				//				labelSlice = append(labelSlice, index)
				//				valueSlice = append(valueSlice, strconv.FormatFloat(value, 'f', -1, 64 ))

			}
		case INDEX_MEMORY_USAGE:
			{
				if container.Spec.Memory.Limit != 0 {
					value = float64(now.Memory.Usage) * 100 / float64(container.Spec.Memory.Limit)
					//					// add label low and high threshold
					//					labelSlice = append(labelSlice, index)
					//					valueSlice = append(valueSlice, strconv.FormatFloat(value, 'f', -1, 64 ))
				}
			}
		case INDEX_NETWORK_TRANSMIT_PACKAGES_TOTAL:
			{
				fmt.Printf("INDEX_NETWORK_TRANSMIT_PACKAGES_TOTAL\n")
				if previous == nil {
					for _, adapter := range now.Network.Interfaces {
						if adapter.Name == "eth0" {
							value = float64(adapter.TxPackets)
							fmt.Printf("INDEX_NETWORK_TRANSMIT_PACKAGES_TOTAL: %v\n", value)
						}
					}
				} else {
					for index, adapter := range now.Network.Interfaces {
						if adapter.Name == "eth0" {
							value = float64(adapter.TxPackets - previous.Network.Interfaces[index].TxPackets)
							fmt.Printf("INDEX_NETWORK_TRANSMIT_PACKAGES_TOTAL: %v\n", value)
							fmt.Printf("debug previous Name %s\n", previous.Network.Interfaces[index].Name)
						}
					}

				}

			}
		case INDEX_NETWORK_RECEIVE_PACKAGES_TOTAL:
			{
				fmt.Printf("INDEX_NETWORK_RECEIVE_PACKAGES_TOTAL")

				if previous == nil {
					for _, adapter := range now.Network.Interfaces {
						if adapter.Name == "eth0" {
							value = float64(adapter.RxPackets)
							fmt.Printf("INDEX_NETWORK_RECEIVE_PACKAGES_TOTAL: %v\n", value)
						}
					}
				} else {
					for index, adapter := range now.Network.Interfaces {
						if adapter.Name == "eth0" {
							value = float64(adapter.RxPackets - previous.Network.Interfaces[index].RxPackets)
							fmt.Printf("INDEX_NETWORK_RECEIVE_PACKAGES_TOTAL: %v\n", value)
							fmt.Printf("debug previous Name %s\n", previous.Network.Interfaces[index].Name)
						}
					}

				}

			}
		default:
			{
				// do nothing
			}
		}

		if len(lowThresholdSValue) != 0 {
			temp := value / lowThreshold
			lowLableSlice := labelSlice
			lowValueSlice := valueSlice
			switch index {
			case INDEX_NETWORK_TRANSMIT_PACKAGES_TOTAL:
			case INDEX_NETWORK_RECEIVE_PACKAGES_TOTAL:
				{
					temp = value / (lowThreshold * float64(nodeNumber))
				}
			}
			//			if temp < 1.0 {
			switch index {
			case INDEX_CPU_USAGE:
				{
					lowLableSlice = append(lowLableSlice, ALERT_NAME)
					lowValueSlice = append(lowValueSlice, ALERT_LOW_CPU)
				}
			case INDEX_MEMORY_USAGE:
				{
					lowLableSlice = append(lowLableSlice, ALERT_NAME)
					lowValueSlice = append(lowValueSlice, ALERT_LOW_MEMORY)
				}
			case INDEX_NETWORK_RECEIVE_PACKAGES_TOTAL:
				{
					// min number of nodes, no need to scale in.
					minNumberSValue := GetContainerEnvValue(containerInfo, MIN_NODE_NUMBER)
					minNumberValue, _ := strconv.ParseInt(minNumberSValue, 0, 64)
					if nodeNumber != -1 && nodeNumber <= minNumberValue {
						temp = 1.0
					}
					lowLableSlice = append(lowLableSlice, ALERT_NAME)
					lowValueSlice = append(lowValueSlice, ALERT_LOW_REV_PACKAGES)
				}
			}
			//			}

			containerIndexUsageDesc := prometheus.NewDesc(CONTAINER_INDEX_PREFIX+index+"_low"+THRESHOLD_CAL_RESULT_SUFFIX, description, lowLableSlice, nil)
			ch <- prometheus.MustNewConstMetric(containerIndexUsageDesc, prometheus.GaugeValue, temp, lowValueSlice...)
		}

		if len(highThresholdSValue) != 0 {
			temp := value / highThreshold
			fmt.Printf("high temp is %v\n", temp)
			fmt.Printf("nodeNumber is %v\n", nodeNumber)
			highLableSlice := labelSlice
			highValueSlice := valueSlice
			switch index {
			case INDEX_NETWORK_TRANSMIT_PACKAGES_TOTAL:
			case INDEX_NETWORK_RECEIVE_PACKAGES_TOTAL:
				{
					temp = value / (highThreshold * float64(nodeNumber))
					fmt.Printf("final high temp is %v\n", temp)
				}
			}
			//			if temp > 1.0 {
			// trigger high alert
			switch index {
			case INDEX_CPU_USAGE:
				{
					highLableSlice = append(highLableSlice, ALERT_NAME)
					highValueSlice = append(highValueSlice, ALERT_HIGH_CPU)
				}
			case INDEX_MEMORY_USAGE:
				{
					highLableSlice = append(highLableSlice, ALERT_NAME)
					highValueSlice = append(highValueSlice, ALERT_HIGH_MEMORY)
				}
			case INDEX_NETWORK_RECEIVE_PACKAGES_TOTAL:
				{
					highLableSlice = append(highLableSlice, ALERT_NAME)
					highValueSlice = append(highValueSlice, ALERT_HIGH_REV_PACKAGES)
				}
			}
			//			}
			containerIndexUsageDesc := prometheus.NewDesc(CONTAINER_INDEX_PREFIX+index+"_high"+THRESHOLD_CAL_RESULT_SUFFIX, description, highLableSlice, nil)
			ch <- prometheus.MustNewConstMetric(containerIndexUsageDesc, prometheus.GaugeValue, temp, highValueSlice...)
		}

	}
}

func (c *PrometheusCollector) IsAlertEnable(container *docker.Container) (result bool) {

	if container != nil {
		envValue := GetContainerEnvValue(container, ALERT_ENABLE)
		temp, err := strconv.ParseBool(envValue)
		if err != nil {
			temp = false
		}
		return temp
	}

	return false
}

func GetContainerEnvValue(containerInfo *docker.Container, envName string) (envValue string) {
	for _, env := range containerInfo.Config.Env {
		if strings.HasPrefix(env, envName+"=") {
			keyvaluepair := strings.Split(env, "=")
			if len(keyvaluepair) == 2 {
				return keyvaluepair[1]
			} else {
				return ""
			}
		}
	}
	return ""
}

func (c *PrometheusCollector) FetchElasticSerachInfo(index, description string, container *info.ContainerInfo, ch chan<- prometheus.Metric) {
	id := container.Name
	name := id
	if len(container.Aliases) > 0 {
		name = container.Aliases[0]
	}

	image := container.Spec.Image

	containerInfo, err := c.client.InspectContainer(name)

	if !c.IsAlertEnable(containerInfo) {
		return
	}

	if err != nil {
		// inspect docker instance failed.
		fmt.Println("inspect docker instance failed.")
	} else {
		ipMap := make(map[string]string)

		// find the ip addresses in this docker.
		for key := range containerInfo.NetworkSettings.Networks {
			fmt.Printf("Find network: %s \n", key)
			cNetwork := containerInfo.NetworkSettings.Networks[key]
			fmt.Printf("Find IPAddress: %s \n", cNetwork.IPAddress)
			ipMap[cNetwork.IPAddress] = strings.TrimSpace(cNetwork.IPAddress)
		}

		appId := GetContainerEnvValue(containerInfo, LINKER_APP_ID)

		url := "http://" + DNS_DOMAINNAME + ":" + LINKER_ELASTICSERACH_PORT + "/_cat/nodes?h=i,rp,m,cpu"
		resp, err := http.Get(url)
		if err != nil {
			fmt.Printf("http get error: %v\n", err)
			return
		}
		defer resp.Body.Close()
		resp_body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			fmt.Printf("read body error: %v\n", err)
			return
		}
		fmt.Printf("body: %v\n", resp_body)

		nodes := strings.Split(string(resp_body), "\n")

		nodeNumber := int64(0)
		memoryTotal := 0.0
		memoryAvgUsage := 0.0
		cpuTotal := 0.0
		cpuAvgUsage := 0.0
		master := ""
		for _, node := range nodes {
			if node != "" {
				nodeNumber++

				node = checkString(node)
				fmt.Printf("!!%s!! \n", node)
				items := strings.Split(node, " ")
				for _, item := range items {
					fmt.Printf("%s \n", item)
				}

				fmt.Printf("Is master: %s\n", items[2])
				if items[2] == "*" {
					if ipMap[strings.TrimSpace(items[0])] != "" {
						master = items[0]
						fmt.Printf("Found the master node, %s\n", ipMap[items[0]])
					} else {
						// not in master node. just return.
						fmt.Println("not in master node. just return.")
						return
					}
				}

				fMemeory, err := strconv.ParseFloat(items[1], 64)

				if err != nil {
					memoryTotal = memoryTotal + 0.0
				} else {
					memoryTotal = memoryTotal + fMemeory
				}

				fCPU, err := strconv.ParseFloat(items[3], 64)
				if err != nil {
					cpuTotal = cpuTotal + 0.0
				} else {
					cpuTotal = cpuTotal + fCPU
				}
			}

		}

		if len(master) <= 0 {
			// not a master node, just return
			return
		}

		if nodeNumber != 0 {
			memoryAvgUsage = memoryTotal / float64(nodeNumber)
			fmt.Printf("memoryAvgUsage: %v", memoryAvgUsage)
			cpuAvgUsage = cpuTotal / float64(nodeNumber)
			fmt.Printf("cpuAvgUsage: %v", cpuAvgUsage)
		} else {
			return
		}

		//		process(INDEX_MEMORY_USAGE, "Usage of Memory on the ElasticSearch Docker instance.", id, image, name, appId, nodeNumber, memoryAvgUsage, containerInfo, ch)
		process(INDEX_CPU_USAGE, "Usage of CPU on the ElasticSearch Docker instance.", id, image, name, appId, nodeNumber, cpuAvgUsage, containerInfo, ch)
	}

}

func process(index, description, id, image, name, appId string, nodeNumber int64, value float64, containerInfo *docker.Container, ch chan<- prometheus.Metric) {

	labels := []string{"id", "image", "name", "service_group_id", "service_group_instance_id", "service_order_id", "app_container_id", "mesos_task_id", "group_id", "app_id", "repair_template_id", "alert"}

	fmt.Println("inspect docker instance successfully.")
	serviceGroupId := GetContainerEnvValue(containerInfo, LINKER_SG_ID)
	serviceGroupInstanceId := GetContainerEnvValue(containerInfo, LINKER_SGI_ID)
	serviceOrderId := GetContainerEnvValue(containerInfo, LINKDER_SO_ID)
	appContainerId := GetContainerEnvValue(containerInfo, LINKER_APP_CONTAINER_ID)
	mesosTaskId := GetContainerEnvValue(containerInfo, LINKER_MESOS_TASK_ID)
	groupId := GetContainerEnvValue(containerInfo, LINKER_GROUP_ID)

	repairTemplateId := GetContainerEnvValue(containerInfo, LINKER_REPAIR_TEMPALTE_ID)
	baseLabelValues := []string{id, image, name, serviceGroupId, serviceGroupInstanceId, serviceOrderId, appContainerId, mesosTaskId, groupId, appId, repairTemplateId, "true"}

	lowThreadoldEnv := index + THRESHOLD_LOW_SUFFIX
	lowThresholdSValue := GetContainerEnvValue(containerInfo, strings.ToUpper(lowThreadoldEnv))
	highThreadoldEnv := index + THRESHOLD_HIGH_SUFFIX
	highThresholdSValue := GetContainerEnvValue(containerInfo, strings.ToUpper(highThreadoldEnv))

	fmt.Printf("high threshold is %s\n", highThresholdSValue)
	fmt.Printf("low threshold is %s\n", lowThresholdSValue)

	lowThreshold := 0.0
	// check if Low Threshold is set.
	if len(lowThresholdSValue) != 0 {
		lowThreshold, _ = strconv.ParseFloat(lowThresholdSValue, 64)
	}
	fmt.Printf("lowThreshold is %v \n", lowThreshold)

	highThreshold := 0.0
	// check if High Threshold is set.
	if len(highThresholdSValue) != 0 {
		highThreshold, _ = strconv.ParseFloat(highThresholdSValue, 64)
	}
	fmt.Printf("highThreshold is %v \n", highThreshold)

	labelSlice := labels[0:len(labels)]
	valueSlice := baseLabelValues[0:len(baseLabelValues)]

	if len(lowThresholdSValue) != 0 {
		lowLableSlice := labelSlice
		lowValueSlice := valueSlice
		temp := value / lowThreshold
		fmt.Printf("low temp is %v\n", temp)

		// min number of nodes, no need to scale in.
		minNumberSValue := GetContainerEnvValue(containerInfo, MIN_NODE_NUMBER)
		minNumberValue, _ := strconv.ParseInt(minNumberSValue, 0, 64)
		if nodeNumber != -1 && nodeNumber <= minNumberValue {
			temp = 1.0
		}

		switch index {
		case INDEX_CPU_USAGE:
			{
				lowLableSlice = append(lowLableSlice, ALERT_NAME)
				lowValueSlice = append(lowValueSlice, ALERT_LOW_CPU)
			}
		case INDEX_MEMORY_USAGE:
			{
				lowLableSlice = append(lowLableSlice, ALERT_NAME)
				lowValueSlice = append(lowValueSlice, ALERT_LOW_MEMORY)
			}
		case INDEX_NETWORK_TRANSMIT_PACKAGE_NUMBER:
			{
				lowLableSlice = append(lowLableSlice, ALERT_NAME)
				lowValueSlice = append(lowValueSlice, ALERT_LOW_TRANSMIT_PACKAGE_NUMBER)
			}
		case INDEX_PGW_CONNECTIONS:
			{
				lowLableSlice = append(lowLableSlice, ALERT_NAME)
				lowValueSlice = append(lowValueSlice, ALERT_PGW_LOW_CONNECTIONS)
			}
		case INDEX_SGW_CONNECTIONS:
			{
				lowLableSlice = append(lowLableSlice, ALERT_NAME)
				lowValueSlice = append(lowValueSlice, ALERT_SGW_LOW_CONNECTIONS)
			}
		}

		containerIndexUsageDesc := prometheus.NewDesc(CONTAINER_INDEX_PREFIX+index+"_low"+THRESHOLD_CAL_RESULT_SUFFIX, description, lowLableSlice, nil)
		ch <- prometheus.MustNewConstMetric(containerIndexUsageDesc, prometheus.GaugeValue, temp, lowValueSlice...)
	}

	if len(highThresholdSValue) != 0 {
		highLableSlice := labelSlice
		highValueSlice := valueSlice
		temp := value / highThreshold
		fmt.Printf("high temp is %v\n", temp)

		// max number of nodes, no need to scale in.
		maxNumberSValue := GetContainerEnvValue(containerInfo, MAX_NODE_NUMBER)
		maxNumberValue, _ := strconv.ParseInt(maxNumberSValue, 0, 64)
		if nodeNumber != -1 && nodeNumber >= maxNumberValue {
			temp = 1.0
		}

		switch index {
		case INDEX_CPU_USAGE:
			{
				highLableSlice = append(highLableSlice, ALERT_NAME)
				highValueSlice = append(highValueSlice, ALERT_HIGH_CPU)
			}
		case INDEX_MEMORY_USAGE:
			{
				highLableSlice = append(highLableSlice, ALERT_NAME)
				highValueSlice = append(highValueSlice, ALERT_HIGH_MEMORY)
			}
		case INDEX_NETWORK_TRANSMIT_PACKAGE_NUMBER:
			{
				highLableSlice = append(highLableSlice, ALERT_NAME)
				highValueSlice = append(highValueSlice, ALERT_HIGH_TRANSMIT_PACKAGE_NUMBER)
			}
		case INDEX_PGW_CONNECTIONS:
			{
				highLableSlice = append(highLableSlice, ALERT_NAME)
				highValueSlice = append(highValueSlice, ALERT_PGW_HIGH_CONNECTIONS)
			}
		case INDEX_SGW_CONNECTIONS:
			{
				highLableSlice = append(highLableSlice, ALERT_NAME)
				highValueSlice = append(highValueSlice, ALERT_SGW_HIGH_CONNECTIONS)
			}
		}

		containerIndexUsageDesc := prometheus.NewDesc(CONTAINER_INDEX_PREFIX+index+"_high"+THRESHOLD_CAL_RESULT_SUFFIX, description, highLableSlice, nil)
		ch <- prometheus.MustNewConstMetric(containerIndexUsageDesc, prometheus.GaugeValue, temp, highValueSlice...)
	}

}

func checkString(line string) string {
	var buffer bytes.Buffer
	flag := true
	for i := 0; i < len(line); i++ {
		if string(line[i]) == " " {
			if flag {
				buffer.WriteByte(line[i])
			}
			flag = false
		} else {
			flag = true
			buffer.WriteByte(line[i])
		}
	}
	return buffer.String()
}

func (c *PrometheusCollector) GetLinkerUDPMonitorInfo(index, description, port, protocol, key1, key2 string, container *info.ContainerInfo, ch chan<- prometheus.Metric) {
	fmt.Println("GetLinkerUDPMonitorInfo %v, %v", index, port)
	id := container.Name
	name := id
	if len(container.Aliases) > 0 {
		name = container.Aliases[0]
	}

	image := container.Spec.Image
	containerInfo, err := c.client.InspectContainer(name)

	if !c.IsAlertEnable(containerInfo) {
		return
	}

	if err != nil {
		// inspect docker instance failed.
		fmt.Println("inspect docker instance failed.")
	} else {
		// find the real port in port mapping.
		rdmPort := ""
		foundFlag := false
		for key := range containerInfo.HostConfig.PortBindings {
			if key.Port() == port && key.Proto() == protocol {
				foundFlag = true
				rdmPort = containerInfo.HostConfig.PortBindings[key][0].HostPort
			}

		}

		fmt.Printf("RdmPort is %s \n", rdmPort)

		if !foundFlag {
			fmt.Printf("Can't port mapping for %s, just return\n", port)
			return
		}

		host := GetContainerEnvValue(containerInfo, "HOST")

		node, packageNumber, err := UdpCall(host, rdmPort, key1, key2)
		if err != nil {
			fmt.Println("UDP call to fetch info failed.", err)
			return
		} else {
			if node == 0 {
				fmt.Printf("Node number is zero! Will not generate monitor info...")
				return
			}

			appId := GetContainerEnvValue(containerInfo, LINKER_APP_ID)
			process(index, description, id, image, name, appId, int64(node), float64(packageNumber/node), containerInfo, ch)
		}
	}
}

func UdpCall(host, port, key1, key2 string) (node, packageNumber int, err error) {
	//	addr, err := net.ResolveUDPAddr("udp", "marathonlb-lb-linkerdns.marathon.mesos:10089")
	//	addr, err := net.ResolveUDPAddr("udp", "192.168.10.95:10294")
	//	addr, err := net.ResolveUDPAddr("udp", "192.168.10.70:31820")
	addr, err := net.ResolveUDPAddr("udp", host+":"+port)

	if err != nil {
		fmt.Println("Can't resolve address: ", err)
		return 0, 0, err
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		fmt.Println("Can't dial: ", err)
		return 0, 0, err
	}
	defer conn.Close()

	fmt.Println("Writing something to server...")
	_, err = conn.Write([]byte("Hello!"))
	if err != nil {
		fmt.Println("failed:", err)
		return 0, 0, err
	}

	var buf = make([]byte, 2048)
	fmt.Println("Try to connecting...")
	n, address, err := conn.ReadFromUDP(buf)
	fmt.Println("Connecting...")
	value := ""
	if address != nil {
		fmt.Println("got message from ", address, " with n = ", n)
		if n > 0 {
			fmt.Println("from address", address, "got message:", string(buf[0:n]), n)
			value = string(buf[0:n])
		}
	}

	node, packageNumber = parseJsonData(value, key1, key2)
	fmt.Printf("node: %d, packageNumber: %d \n", node, packageNumber)
	return node, packageNumber, nil
}

func parseJsonData(data, key1, key2 string) (node int, packageNumber int) {
	jsondata := map[string]interface{}{}

	result := json.NewDecoder(strings.NewReader(data))
	result.Decode(&jsondata)

	jq := jsonq.NewQuery(jsondata)

	node, _ = jq.Int(key1)
	packageNumber, _ = jq.Int(key2)
	return node, packageNumber
}

// Call PGW/SGW monitor
func (c *PrometheusCollector) GetGwMonitorInfo(index, description string, container *info.ContainerInfo, ch chan<- prometheus.Metric) {
	fmt.Printf("GetGwMonitorInfo %v, %v\n", index, container)
	id := container.Name
	name := id
	if len(container.Aliases) > 0 {
		name = container.Aliases[0]
	}

	image := container.Spec.Image
	containerInfo, err := c.client.InspectContainer(name)

	if !c.IsAlertEnable(containerInfo) {
		fmt.Println("Alert not enabled.")
		return
	}

	if err != nil {
		// inspect docker instance failed.
		fmt.Println("inspect docker instance failed.")
	} else {
		host := GetContainerEnvValue(containerInfo, "HOST")
		// TODO change port
		monitorPort := "18089"

		// connections: total connections of all instances
		// instances: number of GW containers
		gwType, connections, instances, err := CallGwMonitor(host, monitorPort)
		fmt.Printf("type %v, conn %v, instances %v, err %v", gwType, connections, instances, err)

		if err != nil {
			fmt.Printf("call GwMonitor to fetch info error: %v \n", err)
			return
		}

		if instances <= 0 {
			fmt.Printf("instances is %d, skip\n", instances)
			return
		}

		appId := GetContainerEnvValue(containerInfo, LINKER_APP_ID)

		switch gwType {
		case "PGW":
			process(INDEX_PGW_CONNECTIONS, "Usage of PGW instance.", id, image, name, appId, int64(instances), float64(connections)/float64(instances), containerInfo, ch)
		case "SGW":
			process(INDEX_SGW_CONNECTIONS, "Usage of SGW instance.", id, image, name, appId, int64(instances), float64(connections)/float64(instances), containerInfo, ch)
		default:
			fmt.Printf("unknown gw type: %s\n", gwType)
		}
	}
}

// Call monitor RESTful API
func CallGwMonitor(ip, port string) (gwType string, connections, instances int, err error) {
	url := "http://" + ip + ":" + port + "/monitor"
	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("get %s error: %v\n", url, err)
		return
	}
	data, _ := ioutil.ReadAll(resp.Body)
	// body json struct
	response := struct {
		Success bool `Success`
		Data    struct {
			Instances   int    `Instances`
			ConnNum     int    `ConnNum`
			MonitorType string `MonitorType`
		} `Data`
		Err string `Err`
	}{}

	err = json.Unmarshal(data, &response)
	if err != nil {
		fmt.Printf("unmarshal json error: %v", err)
		return
	}

	if len(response.Err) > 0 {
		err = errors.New(response.Err)
		fmt.Printf("monitor return error: %s\n", response.Err)
		return
	}

	if response.Success {
		gwType = response.Data.MonitorType
		connections = response.Data.ConnNum
		instances = response.Data.Instances
		return
	}
	return
}

func (c *PrometheusCollector) GetContainerInfo(index, description string, container *info.ContainerInfo, ch chan<- prometheus.Metric, now, previous *info.ContainerStats, nodeNumber int64) {
	labels := []string{"id", "image", "name", "service_group_id", "service_group_instance_id", "service_order_id", "app_container_id", "mesos_task_id", "group_id", "app_id"}

	id := container.Name
	name := id
	if len(container.Aliases) > 0 {
		name = container.Aliases[0]
	}

	image := container.Spec.Image

	containerInfo, err := c.client.InspectContainer(name)

	if err != nil {
		// inspect docker instance failed.
	} else {
		serviceGroupId := GetContainerEnvValue(containerInfo, LINKER_SG_ID)
		serviceGroupInstanceId := GetContainerEnvValue(containerInfo, LINKER_SGI_ID)
		serviceOrderId := GetContainerEnvValue(containerInfo, LINKDER_SO_ID)
		appContainerId := GetContainerEnvValue(containerInfo, LINKER_APP_CONTAINER_ID)
		mesosTaskId := GetContainerEnvValue(containerInfo, LINKER_MESOS_TASK_ID)
		groupId := GetContainerEnvValue(containerInfo, LINKER_GROUP_ID)
		appId := GetContainerEnvValue(containerInfo, LINKER_APP_ID)
		baseLabelValues := []string{id, image, name, serviceGroupId, serviceGroupInstanceId, serviceOrderId, appContainerId, mesosTaskId, groupId, appId}
		value := float64(0)

		labelSlice := labels[0:len(labels)]
		valueSlice := baseLabelValues[0:len(baseLabelValues)]

		// check index type
		switch index {
		case INDEX_CPU_USAGE:
			{
				// Maybe there are more than one network adpater here.
				if previous == nil {
					value = float64(now.Cpu.Usage.Total)
				} else {
					value = float64(now.Cpu.Usage.Total - previous.Cpu.Usage.Total)
					interval := now.Timestamp.UnixNano() - previous.Timestamp.UnixNano()
					value = value * 100.0 / float64(interval)
					fmt.Printf("CPU: mesostask.id=%s , usage=%d \n", mesosTaskId, value)
				}

				// add label low and high threshold
				//				labelSlice = append(labelSlice, index)
				//				valueSlice = append(valueSlice, strconv.FormatFloat(value, 'f', -1, 64))

			}
		case INDEX_MEMORY_USAGE:
			{
				if container.Spec.Memory.Limit != 0 {
					value = float64(now.Memory.Usage) * 100 / float64(container.Spec.Memory.Limit)
					fmt.Printf("Memory: mesostask.id=%s , usage=%d \n", mesosTaskId, value)
					// add label low and high threshold
					//					labelSlice = append(labelSlice, index)
					//					valueSlice = append(valueSlice, strconv.FormatFloat(value, 'f', -1, 64))
				}
			}
		default:
			{
				// do nothing
			}
		}

		containerIndexUsageDesc := prometheus.NewDesc(index+"_tobe_averaged", description, labelSlice, nil)
		ch <- prometheus.MustNewConstMetric(containerIndexUsageDesc, prometheus.GaugeValue, value, valueSlice...)

	}
}
