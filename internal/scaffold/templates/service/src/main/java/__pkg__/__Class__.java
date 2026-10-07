package {{.Package}};

import {{.ServiceClass}};

import org.osgi.service.component.annotations.Component;

/**
 * @author {{.Author}}
 */
@Component(
	property = {
		// TODO enter required service properties
	},
	service = {{simple .ServiceClass}}.class
)
public class {{.ClassName}} implements {{simple .ServiceClass}} {

	// TODO enter required service methods

}
