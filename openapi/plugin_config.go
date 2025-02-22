package openapi

import (
	"bufio"
	"fmt"
	"github.com/dikhan/terraform-provider-openapi/v2/openapi/terraformutils"
	"gopkg.in/yaml.v2"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strconv"
	"strings"
)

// OpenAPIPluginConfigurationFileName defines the file name used for the OpenAPI plugin configuration
const OpenAPIPluginConfigurationFileName = "terraform-provider-openapi.yaml"

const otfVarSwaggerURL = "OTF_VAR_%s_SWAGGER_URL"
const otfVarInsecureSkipVerify = "OTF_INSECURE_SKIP_VERIFY"
const otfVarPluginConfigurationFile = "OTF_VAR_%s_PLUGIN_CONFIGURATION_FILE"

// PluginConfiguration defines the OpenAPI plugin's configuration
type PluginConfiguration struct {
	// ProviderName defines the <provider_name> (should match the provider name of the terraform provider binary; terraform-provider-<provider_name>)
	ProviderName string
	// Configuration defines the reader that contains the plugin's external configuration (located at ~/.terraform.d/plugins)
	// If the plugin configuration file is not present the OTF_VAR_<provider_name>_SWAGGER_URL environment variable will
	// be required when invoking the openapi provider.
	// If at runtime both the OTF_VAR_<provider_name>_SWAGGER_URL as well as the plugin configuration file are present
	// the former takes preference. This allows the user to override the url specified in the configuration file with
	// the value provided in the OTF_VAR_<provider_name>_SWAGGER_URL
	Configuration io.Reader
}

// NewPluginConfiguration creates a new PluginConfiguration
func NewPluginConfiguration(providerName string) (*PluginConfiguration, error) {
	var configurationFile io.Reader
	configurationFilePath, err := getPluginConfigurationPath(providerName)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(configurationFilePath); os.IsNotExist(err) {
		log.Printf("[INFO] open api plugin configuration not present at %s", configurationFilePath)
	} else {
		log.Printf("[INFO] found open api plugin configuration at %s", configurationFilePath)
		file, err := os.Open(configurationFilePath) // #nosec G304
		if err != nil {
			return nil, err
		}
		configurationFile = bufio.NewReader(file)
	}
	return &PluginConfiguration{
		ProviderName:  providerName,
		Configuration: configurationFile,
	}, nil
}

func getPluginConfigurationPath(providerName string) (string, error) {
	pluginConfigurationFileEnvVar := fmt.Sprintf(otfVarPluginConfigurationFile, providerName)
	pluginConfigurationFileEnvVars := []string{pluginConfigurationFileEnvVar, strings.ToUpper(pluginConfigurationFileEnvVar)}
	pluginConfigurationFile, err := terraformutils.MultiEnvDefaultString(pluginConfigurationFileEnvVars, "")
	if err != nil {
		return "", err
	}
	if pluginConfigurationFile != "" {
		return pluginConfigurationFile, nil
	}

	terraformUtils, err := terraformutils.NewTerraformUtils()
	if err != nil {
		return "", err
	}
	expandedTerraformVendorDir, err := terraformUtils.GetTerraformPluginsVendorDir()
	if err != nil {
		return "", err
	}
	configurationFilePath := fmt.Sprintf("%s/%s", expandedTerraformVendorDir, OpenAPIPluginConfigurationFileName)
	return configurationFilePath, nil
}

func (p *PluginConfiguration) getServiceConfiguration() (ServiceConfiguration, error) {
	var pluginConfig PluginConfigSchema
	var pluginConfigV1 = &PluginConfigSchemaV1{}
	var serviceConfig ServiceConfiguration
	var err error

	swaggerURLEnvVar := fmt.Sprintf(otfVarSwaggerURL, p.ProviderName)
	swaggerURLEnvVars := []string{swaggerURLEnvVar, strings.ToUpper(swaggerURLEnvVar)}
	apiDiscoveryURL, err := terraformutils.MultiEnvDefaultString(swaggerURLEnvVars, "")
	if err != nil {
		return nil, err
	}
	// Found OTF_VAR_%s_SWAGGER_URL env variable
	if apiDiscoveryURL != "" {
		log.Printf("[INFO] %s set with value %s", swaggerURLEnvVar, apiDiscoveryURL)
		skipVerify, _ := strconv.ParseBool(os.Getenv(otfVarInsecureSkipVerify))
		log.Printf("[INFO] %s set with value %t", otfVarInsecureSkipVerify, skipVerify)
		pluginConfigV1.Services = map[string]*ServiceConfigV1{}
		pluginConfigV1.Services[p.ProviderName] = NewServiceConfigV1(apiDiscoveryURL, skipVerify, nil)
		serviceConfig, err = pluginConfigV1.GetServiceConfig(p.ProviderName)
		if err != nil {
			return nil, err
		}
	}

	// Falling back to read from plugin configuration reader
	if serviceConfig == nil {
		if p.Configuration != nil {
			source, err := ioutil.ReadAll(p.Configuration)
			if err != nil {
				return nil, fmt.Errorf("failed to read %s configuration file", OpenAPIPluginConfigurationFileName)
			}
			err = yaml.Unmarshal(source, pluginConfigV1)
			if err != nil {
				return nil, fmt.Errorf("failed to unmarshall %s configuration file - error = %s", OpenAPIPluginConfigurationFileName, err)
			}
			pluginConfig = PluginConfigSchema(pluginConfigV1)
			if err = pluginConfig.Validate(); err != nil {
				return nil, fmt.Errorf("error occurred while validating '%s' - error = %s", OpenAPIPluginConfigurationFileName, err)
			}
			serviceConfig, err = pluginConfig.GetServiceConfig(p.ProviderName)
			if err != nil {
				return nil, fmt.Errorf("error occurred when getting service configuration from plugin configuration file %s - error = %s", OpenAPIPluginConfigurationFileName, err)
			}
		}
	}

	log.Printf("[DEBUG] serviceConfig = %+v", serviceConfig)

	if serviceConfig == nil || serviceConfig.GetSwaggerURL() == "" {
		return nil, fmt.Errorf("swagger url not provided, please export OTF_VAR_<provider_name>_SWAGGER_URL env variable with the URL where '%s' service provider is exposing the swagger file OR create a plugin configuration file at ~/.terraform.d/plugins following the Plugin configuration schema specifications", p.ProviderName)
	}

	if err = serviceConfig.Validate(); err != nil {
		return nil, fmt.Errorf("service configuration for '%s' not valid: %s", p.ProviderName, err)
	}

	return serviceConfig, err
}
