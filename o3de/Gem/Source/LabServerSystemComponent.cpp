#include <AzCore/Serialization/SerializeContext.h>
#include <AzCore/Utils/Utils.h>
#include <AzFramework/API/ApplicationAPI.h>

#include "LabServerSystemComponent.h"

#include <LabServer/LabServerTypeIds.h>

namespace LabServer
{
    AZ_COMPONENT_IMPL(LabServerSystemComponent, "LabServerSystemComponent",
        LabServerSystemComponentTypeId);

    void LabServerSystemComponent::Reflect(AZ::ReflectContext* context)
    {
        if (auto serializeContext = azrtti_cast<AZ::SerializeContext*>(context))
        {
            serializeContext->Class<LabServerSystemComponent, AZ::Component>()
                ->Version(0)
                ;
        }
    }

    void LabServerSystemComponent::GetProvidedServices(AZ::ComponentDescriptor::DependencyArrayType& provided)
    {
        provided.push_back(AZ_CRC_CE("LabServerService"));
    }

    void LabServerSystemComponent::GetIncompatibleServices(AZ::ComponentDescriptor::DependencyArrayType& incompatible)
    {
        incompatible.push_back(AZ_CRC_CE("LabServerService"));
    }

    void LabServerSystemComponent::GetRequiredServices([[maybe_unused]] AZ::ComponentDescriptor::DependencyArrayType& required)
    {
    }

    void LabServerSystemComponent::GetDependentServices([[maybe_unused]] AZ::ComponentDescriptor::DependencyArrayType& dependent)
    {
    }

    LabServerSystemComponent::LabServerSystemComponent()
    {
        if (LabServerInterface::Get() == nullptr)
        {
            LabServerInterface::Register(this);
        }
    }

    LabServerSystemComponent::~LabServerSystemComponent()
    {
        if (LabServerInterface::Get() == this)
        {
            LabServerInterface::Unregister(this);
        }
    }

    void LabServerSystemComponent::Init()
    {
    }

    void LabServerSystemComponent::Activate()
    {
        LabServerRequestBus::Handler::BusConnect();
        // The same module loads into every launcher and tool: only a server launcher runs the game server.
        AZ::IO::FixedMaxPath exe;
        AZ::Utils::GetExecutablePath(exe.Native().data(), exe.Native().capacity());
        if (AZStd::string_view(exe.c_str()).find("ServerLauncher") == AZStd::string_view::npos)
        {
            return;
        }
        m_game = AZStd::make_unique<LabGame>();
        if (!m_game->Start())
        {
            m_exitAsked = true;
            AzFramework::ApplicationRequests::Bus::Broadcast(&AzFramework::ApplicationRequests::ExitMainLoop);
            return;
        }
        AZ::TickBus::Handler::BusConnect();
    }

    void LabServerSystemComponent::Deactivate()
    {
        AZ::TickBus::Handler::BusDisconnect();
        m_game.reset();
        LabServerRequestBus::Handler::BusDisconnect();
    }

    void LabServerSystemComponent::OnTick([[maybe_unused]] float deltaTime, [[maybe_unused]] AZ::ScriptTimePoint time)
    {
        m_game->Poll();
        if (m_game->Done() && !m_exitAsked)
        {
            m_exitAsked = true;
            AzFramework::ApplicationRequests::Bus::Broadcast(&AzFramework::ApplicationRequests::ExitMainLoop);
        }
    }
}
