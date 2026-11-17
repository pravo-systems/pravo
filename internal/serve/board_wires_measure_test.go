package serve

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/schema"
)

// wireTruth are references that the documentation of the providers writes, as type.attribute: the type it reads.
var wireTruth = map[string]string{
	"aws_subnet.vpc_id":                                         "aws_vpc",
	"aws_security_group.vpc_id":                                 "aws_vpc",
	"aws_instance.subnet_id":                                    "aws_subnet",
	"aws_instance.vpc_security_group_ids":                       "aws_security_group",
	"aws_instance.iam_instance_profile":                         "aws_iam_instance_profile",
	"aws_instance.key_name":                                     "aws_key_pair",
	"aws_db_instance.db_subnet_group_name":                      "aws_db_subnet_group",
	"aws_db_instance.vpc_security_group_ids":                    "aws_security_group",
	"aws_db_instance.kms_key_id":                                "aws_kms_key",
	"aws_db_instance.parameter_group_name":                      "aws_db_parameter_group",
	"aws_db_subnet_group.subnet_ids":                            "aws_subnet",
	"aws_route_table.vpc_id":                                    "aws_vpc",
	"aws_route_table_association.subnet_id":                     "aws_subnet",
	"aws_route_table_association.route_table_id":                "aws_route_table",
	"aws_route.route_table_id":                                  "aws_route_table",
	"aws_route.nat_gateway_id":                                  "aws_nat_gateway",
	"aws_internet_gateway.vpc_id":                               "aws_vpc",
	"aws_nat_gateway.subnet_id":                                 "aws_subnet",
	"aws_nat_gateway.allocation_id":                             "aws_eip",
	"aws_lb.subnets":                                            "aws_subnet",
	"aws_lb.security_groups":                                    "aws_security_group",
	"aws_lb_listener.load_balancer_arn":                         "aws_lb",
	"aws_lb_target_group.vpc_id":                                "aws_vpc",
	"aws_lb_target_group_attachment.target_group_arn":           "aws_lb_target_group",
	"aws_iam_role_policy_attachment.role":                       "aws_iam_role",
	"aws_iam_role_policy_attachment.policy_arn":                 "aws_iam_policy",
	"aws_iam_instance_profile.role":                             "aws_iam_role",
	"aws_iam_role_policy.role":                                  "aws_iam_role",
	"aws_lambda_function.role":                                  "aws_iam_role",
	"aws_lambda_permission.function_name":                       "aws_lambda_function",
	"aws_lambda_event_source_mapping.function_name":             "aws_lambda_function",
	"aws_s3_bucket_policy.bucket":                               "aws_s3_bucket",
	"aws_s3_bucket_versioning.bucket":                           "aws_s3_bucket",
	"aws_s3_bucket_public_access_block.bucket":                  "aws_s3_bucket",
	"aws_sqs_queue_policy.queue_url":                            "aws_sqs_queue",
	"aws_sns_topic_subscription.topic_arn":                      "aws_sns_topic",
	"aws_ecs_service.cluster":                                   "aws_ecs_cluster",
	"aws_ecs_service.task_definition":                           "aws_ecs_task_definition",
	"aws_eks_node_group.cluster_name":                           "aws_eks_cluster",
	"aws_eks_node_group.node_role_arn":                          "aws_iam_role",
	"aws_eks_node_group.subnet_ids":                             "aws_subnet",
	"aws_eks_cluster.role_arn":                                  "aws_iam_role",
	"aws_cloudwatch_log_subscription_filter.log_group_name":     "aws_cloudwatch_log_group",
	"aws_route53_record.zone_id":                                "aws_route53_zone",
	"aws_acm_certificate_validation.certificate_arn":            "aws_acm_certificate",
	"aws_kms_alias.target_key_id":                               "aws_kms_key",
	"aws_eip.instance":                                          "aws_instance",
	"aws_volume_attachment.instance_id":                         "aws_instance",
	"aws_volume_attachment.volume_id":                           "aws_ebs_volume",
	"aws_elasticache_cluster.subnet_group_name":                 "aws_elasticache_subnet_group",
	"aws_vpc_endpoint.vpc_id":                                   "aws_vpc",
	"aws_security_group_rule.security_group_id":                 "aws_security_group",
	"aws_vpc_security_group_ingress_rule.security_group_id":     "aws_security_group",
	"aws_ecr_lifecycle_policy.repository":                       "aws_ecr_repository",
	"aws_api_gateway_resource.rest_api_id":                      "aws_api_gateway_rest_api",
	"aws_secretsmanager_secret_version.secret_id":               "aws_secretsmanager_secret",
	"aws_dynamodb_table_item.table_name":                        "aws_dynamodb_table",
	"aws_autoscaling_group.launch_configuration":                "aws_launch_configuration",
	"aws_autoscaling_group.target_group_arns":                   "aws_lb_target_group",
	"google_compute_subnetwork.network":                         "google_compute_network",
	"google_compute_firewall.network":                           "google_compute_network",
	"google_compute_router.network":                             "google_compute_network",
	"google_compute_router_nat.router":                          "google_compute_router",
	"google_compute_attached_disk.disk":                         "google_compute_disk",
	"google_compute_attached_disk.instance":                     "google_compute_instance",
	"google_compute_target_http_proxy.url_map":                  "google_compute_url_map",
	"google_compute_backend_service.health_checks":              "google_compute_health_check",
	"google_container_node_pool.cluster":                        "google_container_cluster",
	"google_container_cluster.network":                          "google_compute_network",
	"google_container_cluster.subnetwork":                       "google_compute_subnetwork",
	"google_sql_database.instance":                              "google_sql_database_instance",
	"google_sql_user.instance":                                  "google_sql_database_instance",
	"google_storage_bucket_iam_member.bucket":                   "google_storage_bucket",
	"google_storage_bucket_object.bucket":                       "google_storage_bucket",
	"google_pubsub_subscription.topic":                          "google_pubsub_topic",
	"google_pubsub_topic_iam_member.topic":                      "google_pubsub_topic",
	"google_service_account_key.service_account_id":             "google_service_account",
	"google_kms_crypto_key.key_ring":                            "google_kms_key_ring",
	"google_kms_crypto_key_iam_member.crypto_key_id":            "google_kms_crypto_key",
	"google_dns_record_set.managed_zone":                        "google_dns_managed_zone",
	"google_cloud_run_service_iam_member.service":               "google_cloud_run_service",
	"google_bigquery_table.dataset_id":                          "google_bigquery_dataset",
	"google_secret_manager_secret_version.secret":               "google_secret_manager_secret",
	"google_vpc_access_connector.network":                       "google_compute_network",
	"google_compute_instance_group_manager.target_pools":        "google_compute_target_pool",
	"google_compute_region_backend_service.health_checks":       "google_compute_health_check",
	"google_cloudfunctions2_function_iam_member.cloud_function": "google_cloudfunctions2_function",
}

// wireHeldOut are more documented references, written down after the rules were tuned on wireTruth and not used to tune
// them: the honest measure of the guess.
var wireHeldOut = map[string]string{
	"aws_network_acl.vpc_id":                                    "aws_vpc",
	"aws_network_acl_association.network_acl_id":                "aws_network_acl",
	"aws_network_acl_association.subnet_id":                     "aws_subnet",
	"aws_vpc_peering_connection.peer_vpc_id":                    "aws_vpc",
	"aws_vpc_peering_connection.vpc_id":                         "aws_vpc",
	"aws_ec2_transit_gateway_vpc_attachment.transit_gateway_id": "aws_ec2_transit_gateway",
	"aws_ec2_transit_gateway_vpc_attachment.subnet_ids":         "aws_subnet",
	"aws_flow_log.log_destination":                              "aws_cloudwatch_log_group",
	"aws_flow_log.vpc_id":                                       "aws_vpc",
	"aws_flow_log.iam_role_arn":                                 "aws_iam_role",
	"aws_efs_mount_target.file_system_id":                       "aws_efs_file_system",
	"aws_efs_mount_target.subnet_id":                            "aws_subnet",
	"aws_efs_access_point.file_system_id":                       "aws_efs_file_system",
	"aws_rds_cluster_instance.cluster_identifier":               "aws_rds_cluster",
	"aws_rds_cluster.db_subnet_group_name":                      "aws_db_subnet_group",
	"aws_cloudwatch_metric_alarm.alarm_actions":                 "aws_sns_topic",
	"aws_sfn_state_machine.role_arn":                            "aws_iam_role",
	"aws_codebuild_project.service_role":                        "aws_iam_role",
	"aws_api_gateway_method.rest_api_id":                        "aws_api_gateway_rest_api",
	"aws_api_gateway_method.resource_id":                        "aws_api_gateway_resource",
	"aws_api_gateway_integration.rest_api_id":                   "aws_api_gateway_rest_api",
	"aws_apigatewayv2_stage.api_id":                             "aws_apigatewayv2_api",
	"aws_apigatewayv2_route.api_id":                             "aws_apigatewayv2_api",
	"aws_cloudwatch_event_target.rule":                          "aws_cloudwatch_event_rule",
	"aws_lambda_alias.function_name":                            "aws_lambda_function",
	"aws_sqs_queue_redrive_policy.queue_url":                    "aws_sqs_queue",
	"aws_s3_bucket_notification.bucket":                         "aws_s3_bucket",
	"aws_s3_object.bucket":                                      "aws_s3_bucket",
	"aws_s3_bucket_server_side_encryption_configuration.bucket": "aws_s3_bucket",
	"aws_iam_user_policy_attachment.user":                       "aws_iam_user",
	"aws_iam_group_membership.group":                            "aws_iam_group",
	"aws_iam_access_key.user":                                   "aws_iam_user",
	"aws_ecs_service.iam_role":                                  "aws_iam_role",
	"aws_launch_template.vpc_security_group_ids":                "aws_security_group",
	"aws_launch_template.key_name":                              "aws_key_pair",
	"aws_elasticache_replication_group.subnet_group_name":       "aws_elasticache_subnet_group",
	"aws_redshift_cluster.cluster_subnet_group_name":            "aws_redshift_subnet_group",
	"aws_wafv2_web_acl_association.web_acl_arn":                 "aws_wafv2_web_acl",
	"aws_db_instance.monitoring_role_arn":                       "aws_iam_role",
	"aws_ecs_task_definition.execution_role_arn":                "aws_iam_role",
	"aws_ecs_task_definition.task_role_arn":                     "aws_iam_role",
	"google_compute_route.network":                              "google_compute_network",
	"google_compute_router_interface.router":                    "google_compute_router",
	"google_compute_instance_group.network":                     "google_compute_network",
	"google_compute_target_https_proxy.url_map":                 "google_compute_url_map",
	"google_compute_target_https_proxy.ssl_certificates":        "google_compute_ssl_certificate",
	"google_compute_global_forwarding_rule.ip_address":          "google_compute_global_address",
	"google_compute_disk_resource_policy_attachment.disk":       "google_compute_disk",
	"google_compute_backend_bucket.bucket_name":                 "google_storage_bucket",
	"google_compute_network_peering.network":                    "google_compute_network",
	"google_compute_network_peering.peer_network":               "google_compute_network",
	"google_sql_ssl_cert.instance":                              "google_sql_database_instance",
	"google_storage_bucket_acl.bucket":                          "google_storage_bucket",
	"google_storage_notification.bucket":                        "google_storage_bucket",
	"google_storage_notification.topic":                         "google_pubsub_topic",
	"google_pubsub_subscription_iam_member.subscription":        "google_pubsub_subscription",
	"google_bigquery_dataset_iam_member.dataset_id":             "google_bigquery_dataset",
	"google_bigquery_table_iam_member.table_id":                 "google_bigquery_table",
	"google_service_account_iam_member.service_account_id":      "google_service_account",
	"google_kms_key_ring_iam_member.key_ring_id":                "google_kms_key_ring",
	"google_secret_manager_secret_iam_member.secret_id":         "google_secret_manager_secret",
	"google_compute_subnetwork_iam_member.subnetwork":           "google_compute_subnetwork",
	"google_compute_instance_iam_member.instance_name":          "google_compute_instance",
	"google_artifact_registry_repository_iam_member.repository": "google_artifact_registry_repository",
	"google_spanner_database.instance":                          "google_spanner_instance",
	"google_bigtable_table.instance_name":                       "google_bigtable_instance",
	"google_redis_instance.authorized_network":                  "google_compute_network",
	"google_compute_region_network_endpoint_group.network":      "google_compute_network",
	"google_compute_region_network_endpoint_group.subnetwork":   "google_compute_subnetwork",
}

// TestBoardWiresMeasure runs the guess of the wires on the schemas of real providers, given as packs of the schema cache
// in PRAVO_BOARD_PACKS (comma separated), and says how much of it holds: how many attributes look like references,
// how many of those find a type, how many find exactly one, and how many of the references the documentation writes
// it finds, and finds first. It is a measurement, not a check: it fails on nothing.
func TestBoardWiresMeasure(t *testing.T) {
	packs := os.Getenv("PRAVO_BOARD_PACKS")
	if packs == "" {
		t.Skip("PRAVO_BOARD_PACKS names no schema packs")
	}
	for _, path := range strings.Split(packs, ",") {
		ps, err := schema.OpenPack(path)
		if err != nil {
			t.Fatal(err)
		}
		names := ps.SortedResourceNames()
		ix := newWireIndex(names, func(typ string) *schema.Block {
			if s := ps.Resource(typ); s != nil {
				return s.Block
			}
			return nil
		})
		var shaped, suffixed, found, one, typesWith, sfxFound, sfxOne int
		unmatched := map[string]int{}
		for _, typ := range names {
			s := ps.Resource(typ)
			if s == nil || s.Block == nil {
				continue
			}
			any := false
			for _, a := range s.Block.Attributes {
				ok, _ := refShaped(a)
				if !ok {
					continue
				}
				shaped++
				sfx := false
				for _, x := range wireSuffixes {
					if strings.HasSuffix(a.Name, x.suffix) {
						sfx = true
					}
				}
				h := ix.wireOf(typ, a)
				if sfx {
					suffixed++
				}
				if h != nil {
					found++
					any = true
					if len(h.To) == 1 {
						one++
					}
					if sfx {
						sfxFound++
						if len(h.To) == 1 {
							sfxOne++
						}
					}
				} else if sfx {
					unmatched[a.Name]++
				}
			}
			if any {
				typesWith++
			}
		}
		for _, set := range []struct {
			name string
			refs map[string]string
		}{{"tuned on", wireTruth}, {"held out", wireHeldOut}} {
			var hit, first, asked int
			var misses []string
			for k, want := range set.refs {
				typ, attr, _ := strings.Cut(k, ".")
				if !strings.HasPrefix(typ, prefixOf(names[0])+"_") {
					continue
				}
				asked++
				s := ps.Resource(typ)
				if s == nil || s.Block == nil || s.Block.Attribute(attr) == nil {
					misses = append(misses, k+" (not in the schema)")
					continue
				}
				h := ix.wireOf(typ, s.Block.Attribute(attr))
				switch {
				case h == nil:
					misses = append(misses, k+" -> nothing")
				case h.To[0].Type == want:
					hit++
					first++
				default:
					in := false
					for _, x := range h.To {
						if x.Type == want {
							in = true
						}
					}
					if in {
						hit++
					}
					misses = append(misses, fmt.Sprintf("%s -> %v", k, h.types()))
				}
			}
			sort.Strings(misses)
			t.Logf("  documented references (%s): %d asked, %d found among the offers, %d found first", set.name, asked, hit, first)
			for _, m := range misses {
				t.Logf("    miss: %s", m)
			}
		}
		type kv struct {
			k string
			n int
		}
		var top []kv
		for k, n := range unmatched {
			top = append(top, kv{k, n})
		}
		sort.Slice(top, func(i, j int) bool { return top[i].n > top[j].n || top[i].n == top[j].n && top[i].k < top[j].k })
		if len(top) > 25 {
			top = top[:25]
		}
		t.Logf("%s: %d resource types, %d with a wire found", path, len(names), typesWith)
		t.Logf("  settable string attributes %d, of them with a reference ending (_id, _arn, _name, ...) %d", shaped, suffixed)
		t.Logf("  wire found for %d attributes, %d of them to exactly one type; of those with an ending %d found, %d to one type", found, one, sfxFound, sfxOne)
		t.Logf("  most common reference endings with no type: %v", top)
	}
}
